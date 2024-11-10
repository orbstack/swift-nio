package domainproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/tlsutil"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const upstreamDialTimeout = 500 * time.Millisecond

var upstreamProbePorts = [...]int{80, 443}

type MdnsContextKey int

const (
	MdnsContextKeyDownstream MdnsContextKey = iota
)

type ProxyCallbacks interface {
	// takes name or IP string
	GetUpstreamByHost(host string, v4 bool) (domainproxytypes.Upstream, error)
	GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error)
	GetMark(upstream domainproxytypes.Upstream) int

	NftableName() string
	NfqueueFlags() uint32
	NfqueueMarkReject(mark uint32) uint32
	NfqueueMarkSkip(mark uint32) uint32
}

type DomainTLSProxy struct {
	cb ProxyCallbacks

	tlsController *tlsutil.TLSController
	tproxy        *bpf.Tproxy
}

func NewDomainTLSProxy(host *hclient.Client, cb ProxyCallbacks) (*DomainTLSProxy, error) {
	tlsController, err := tlsutil.NewTLSController(host)
	if err != nil {
		return nil, err
	}

	return &DomainTLSProxy{
		tlsController: tlsController,
		cb:            cb,
	}, nil
}

func (p *DomainTLSProxy) getReverseProxyServer(https bool) *http.Server {
	httpProxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			err := p.rewriteRequest(r, https)
			if err != nil {
				logrus.WithError(err).Error("failed to rewrite request")
			}
		},
		Transport: &http.Transport{
			DialContext: p.dialUpstream,
			// establishing conns is cheap locally
			// do not limit MaxConnsPerHost in case of load testing
			IdleConnTimeout: 5 * time.Second,
			// allow more idle conns to avoid TIME_WAIT hogging all ports under load test (default 2)
			// otherwise we get "connect: cannot assign requested address" after too long
			MaxIdleConnsPerHost: 200,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
		ErrorHandler: p.handleError,
	}

	httpServer := &http.Server{
		Handler: httpProxy,
		TLSConfig: &tls.Config{
			GetCertificate: func(hlo *tls.ClientHelloInfo) (*tls.Certificate, error) {
				if !strings.HasSuffix(hlo.ServerName, ".local") {
					return nil, nil
				}

				return p.tlsController.MakeCertForHost(hlo.ServerName)
			},
		},
	}

	return httpServer
}

func (p *DomainTLSProxy) Start(ip4, ip6 string, subnet4, subnet6 netip.Prefix) error {
	err := p.tlsController.LoadRoot()
	if err != nil {
		return err
	}

	err = p.startQueue()
	if err != nil {
		return fmt.Errorf("start queue: %w", err)
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}
	loTlsProxyAddr4, err := netlink.ParseAddr(ip4 + "/32")
	if err != nil {
		return err
	}
	err = netlink.AddrAdd(lo, loTlsProxyAddr4)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	loTlsProxyAddr6, err := netlink.ParseAddr(ip6 + "/128")
	if err != nil {
		return err
	}
	err = netlink.AddrAdd(lo, loTlsProxyAddr6)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}

	lcfg := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err2 error
			err := c.Control(func(fd uintptr) {
				// Go sets SO_REUSEADDR by default
				// we need IP_TRANSPARENT to be able to receive packets destined to a non-local ip, even though we're assigning this socket with bpf
				err2 = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TRANSPARENT, 1)
			})
			if err != nil {
				return err
			}

			return err2
		},
	}

	ln4, err := lcfg.Listen(context.TODO(), "tcp", net.JoinHostPort(ip4, "0"))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	ln6, err := lcfg.Listen(context.TODO(), "tcp", net.JoinHostPort(ip6, "0"))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	tproxy, err := bpf.NewTproxy(subnet4, subnet6, 443)
	if err != nil {
		return fmt.Errorf("tls domainproxy: failed to create tproxy bpf: %w", err)
	}

	ln4RawConn, err := ln4.(syscall.Conn).SyscallConn()
	if err != nil {
		return fmt.Errorf("failed to get rawconn from listener: %w", err)
	}
	err = util.UseRawConn(ln4RawConn, func(fd int) error {
		return tproxy.SetSock4(uint64(fd))
	})
	if err != nil {
		return fmt.Errorf("failed to set tproxy socket: %w", err)
	}

	ln6RawConn, err := ln6.(syscall.Conn).SyscallConn()
	if err != nil {
		return fmt.Errorf("failed to get file from listener: %w", err)
	}
	err = util.UseRawConn(ln6RawConn, func(fd int) error {
		return tproxy.SetSock6(uint64(fd))
	})
	if err != nil {
		return fmt.Errorf("failed to set tproxy socket: %w", err)
	}

	err = tproxy.AttachNetNsFromPath("/proc/thread-self/ns/net")
	if err != nil {
		return fmt.Errorf("failed to attach tproxy to netns: %w", err)
	}

	p.tproxy = tproxy

	httpLn4 := util.NewDispatchedListener(ln4.Addr)
	httpLn6 := util.NewDispatchedListener(ln6.Addr)

	httpServer := p.getReverseProxyServer( /*https*/ false)

	go func() {
		err := httpServer.ServeTLS(httpLn4, "", "")
		if err != nil {
			logrus.WithError(err).Error("domainTLSProxy: serve tls failed")
		}
	}()
	go func() {
		err := httpServer.ServeTLS(httpLn6, "", "")
		if err != nil {
			logrus.WithError(err).Error("domainTLSProxy: serve tls failed")
		}
	}()

	httpsLn4 := util.NewDispatchedListener(ln4.Addr)
	httpsLn6 := util.NewDispatchedListener(ln6.Addr)

	httpsServer := p.getReverseProxyServer( /*https*/ true)

	go func() {
		err := httpsServer.ServeTLS(httpsLn4, "", "")
		if err != nil {
			logrus.WithError(err).Error("domainTLSProxy: serve tls failed")
		}
	}()
	go func() {
		err := httpsServer.ServeTLS(httpsLn6, "", "")
		if err != nil {
			logrus.WithError(err).Error("domainTLSProxy: serve tls failed")
		}
	}()

	go p.runConnectionDispatcher(httpLn4, httpsLn4, ln4)
	go p.runConnectionDispatcher(httpLn6, httpsLn6, ln6)

	return nil
}

// warning: caller must check and skip this in hairpinning cases
func dialerForTransparentBind(bindIP net.IP, mark int) *net.Dialer {
	var sa unix.Sockaddr
	if ip4 := bindIP.To4(); ip4 != nil {
		sa4 := &unix.SockaddrInet4{Port: 0}
		copy(sa4.Addr[:], ip4)
		sa = sa4
	} else {
		sa6 := &unix.SockaddrInet6{Port: 0}
		copy(sa6.Addr[:], bindIP)
		sa = sa6
	}

	return &net.Dialer{
		ControlContext: func(ctx context.Context, network, address string, c syscall.RawConn) error {
			var retErr error
			err2 := c.Control(func(fd uintptr) {
				// set IP_TRANSPARENT to be able to bind to any IP
				// IP_FREEBIND doesn't work for some reason
				err := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TRANSPARENT, 1)
				if err != nil {
					retErr = fmt.Errorf("failed to set opt 1: %w", err)
					return
				}

				// set SO_MARK to prevent TPROXY routing loop (since is also going to the dest IP)
				// also, this mark provides routing for the return path when we spoof source IP
				err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
				if err != nil {
					retErr = fmt.Errorf("failed to set opt 2: %w", err)
					return
				}

				// bind to local address, spoof source IP of client (but not port)
				err = unix.Bind(int(fd), sa)
				if err != nil {
					// just a warning. continue but with wrong src IP
					logrus.WithError(err).WithFields(logrus.Fields{"ip": bindIP, "sa": sa}).Warn("failed to bind to laddr")
				}
			})
			if err2 != nil {
				return err2
			}
			return retErr
		},
	}
}

func (p *DomainTLSProxy) runConnectionDispatcher(httpHandler *util.DispatchedListener, httpsHandler *util.DispatchedListener, listen net.Listener) {
	for {
		conn, err := listen.Accept()
		if err != nil {
			// pass through errors. don't need to check if this is successful because it's fine if they're already closed
			httpHandler.SubmitErr(err)
			httpsHandler.SubmitErr(err)
			return
		}

		if httpHandler.Closed() || httpsHandler.Closed() {
			return
		}

		go func() {
			err := p.dispatchIncomingConn(httpHandler, httpsHandler, conn)
			if err != nil {
				logrus.WithError(err).Error("failed to dispatch incoming conn")
			}
		}()
	}
}

func (p *DomainTLSProxy) dispatchIncomingConn(httpHandler *util.DispatchedListener, httpsHandler *util.DispatchedListener, conn net.Conn) (retErr error) {
	// this function just lets us directly passthrough to an existing ssl server. this should be removed soon in favor of port probing
	defer func() {
		if retErr != nil {
			conn.Close()
		}
	}()

	downstreamIP := conn.RemoteAddr().(*net.TCPAddr).IP
	is4 := downstreamIP.To4() != nil

	// this works because we never change the dest, only hook sk_assign (like tproxy but sillier)
	destHost, destPort, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return fmt.Errorf("split host/port: %w", err)
	}

	upstream, err := p.cb.GetUpstreamByHost(destHost, is4)
	if err != nil {
		return fmt.Errorf("get upstream: %w", err)
	}
	mark := p.cb.GetMark(upstream)

	// attempt to make a connection to port 443 to do reterm
	// since these are local containers, connection should fail fast and return RST (-ECONNREFUSED)
	// EXCEPT: if dest is a machine and user installed ufw, then ufw will drop the packet and we'll get a timeout
	//   * workaround: short connection timeout
	//     * this works: if load test is causing listen backlog to be full, we will get immediate RST because port is open in firewall
	// still need to bind to host to get correct cfwd behavior, especially for 443->8443 or 443->https_port case
	// TODO: do with probing
	https := false
	// skip for hairpinning (would cause src == dst IP)
	var dialer *net.Dialer
	if !downstreamIP.Equal(upstream.IP) {
		dialer = dialerForTransparentBind(downstreamIP, mark)
	} else {
		dialer = &net.Dialer{}
	}
	dialer.Timeout = upstreamDialTimeout
	upstreamConn, err := dialer.DialContext(context.Background(), "tcp", net.JoinHostPort(upstream.IP.String(), destPort))
	if err == nil {
		upstreamConn.Close()
		https = true
	}

	if https {
		return httpsHandler.SubmitConn(conn)
	} else {
		return httpHandler.SubmitConn(conn)
	}
}

func (p *DomainTLSProxy) rewriteRequest(r *httputil.ProxyRequest, https bool) error {
	host := r.In.Host
	if host == "" {
		host = r.In.TLS.ServerName
	}

	scheme := "http"
	if https {
		scheme = "https"
	}

	r.SetURL(&url.URL{
		Scheme: scheme,
		// Host is mandatory
		// always use SNI for upstream, so we can pass through any Host header
		Host: host,
		// SetURL takes *base* path
		Path: "/",
	})

	r.Out.Host = host
	r.Out.Header["Forwarded"] = r.In.Header["Forwarded"]
	r.Out.Header["X-Forwarded-For"] = r.In.Header["X-Forwarded-For"]
	r.SetXForwarded()

	// downstream = client
	downstreamAddrStr, _, err := net.SplitHostPort(r.In.RemoteAddr)
	if err != nil {
		return err
	}
	downstreamIP := net.ParseIP(downstreamAddrStr)
	if downstreamIP == nil {
		return fmt.Errorf("parse ip: %s", downstreamAddrStr)
	}

	newContext := context.WithValue(r.Out.Context(), MdnsContextKeyDownstream, downstreamIP)
	r.Out = r.Out.WithContext(newContext)

	// embef local addr in

	return nil
}

func (p *DomainTLSProxy) dialUpstream(ctx context.Context, network, addr string) (net.Conn, error) {
	dialHost, dialPort, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	downstreamIP := ctx.Value(MdnsContextKeyDownstream)
	is4 := true
	if downstreamIP != nil {
		is4 = downstreamIP.(net.IP).To4() != nil
	}

	upstream, err := p.cb.GetUpstreamByHost(dialHost, is4)
	if err != nil {
		return nil, err
	}

	// fall back to normal dialer
	// namely, this is used for hairpin, ie when a machine makes a request to its own domainproxy ip
	var dialer *net.Dialer
	if downstreamIP, ok := downstreamIP.(net.IP); ok && downstreamIP != nil && !downstreamIP.Equal(upstream.IP) {
		dialer = dialerForTransparentBind(downstreamIP, p.cb.GetMark(upstream))
	} else {
		dialer = &net.Dialer{}
	}

	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(upstream.IP.String(), dialPort))
}

// the default action with no handler is to send a 502 with no content and to log
func (p *DomainTLSProxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	// debug log so no spamming for users
	logrus.WithError(err).Debug("domainTLSProxy failed to dial upstream")
	w.WriteHeader(http.StatusBadGateway)
	http.ServeContent(w, r, "", time.UnixMilli(0), bytes.NewReader(
		[]byte(fmt.Sprintf("502 Bad Gateway\nOrbStack proxy error: %v\n", err)),
	))

	// on ECONNREFUSED, delete from probed set, so that nfqueue returns RST again
	if errors.Is(err, unix.ECONNREFUSED) {
		err2 := p.handleConnRefused(r)
		if err2 != nil {
			logrus.WithError(err2).Error("failed to handle conn refused")
		}
	}
}

func (p *DomainTLSProxy) handleConnRefused(r *http.Request) error {
	// get domainproxy IP from local addr
	localAddr := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	domainIP := localAddr.IP

	setName := "domainproxy4_probed"
	if domainIP.To4() == nil {
		setName = "domainproxy6_probed"
	}
	err2 := nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetDeleteByName(conn, table, setName, nft.IP(domainIP))
	})
	// ENOENT = raced with another ECONNREFUSED to remove from set
	if err2 != nil && !errors.Is(err2, unix.ENOENT) {
		return fmt.Errorf("delete from probed set: %w", err2)
	}

	return nil
}
