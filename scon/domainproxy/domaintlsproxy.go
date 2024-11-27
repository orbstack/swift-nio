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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/florianl/go-nfqueue"
	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/tlsutil"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const httpsDialTimeout = 500 * time.Millisecond

type MdnsContextKey int

const (
	MdnsContextKeyDownstream MdnsContextKey = iota
	MdnsContextKeyUpstream
	MdnsContextKeyProxyUpstream
)

var (
	SconHostBridgeIP4 net.IP
	SconHostBridgeIP6 net.IP
)

func init() {
	SconHostBridgeIP4 = net.ParseIP(netconf.SconHostBridgeIP4)
	SconHostBridgeIP6 = net.ParseIP(netconf.SconHostBridgeIP6)
}

type ProxyCallbacks interface {
	// takes name or IP string
	GetUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error)
	GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error)
	GetMark(upstream domainproxytypes.Upstream) int

	NftableName() string
	NfqueueMarkReject(mark uint32) uint32
	NfqueueMarkSkip(mark uint32) uint32

	GetMachineOpenPorts(machineID string) (map[uint16]struct{}, error)
	GetContainerOpenPorts(containerID string) (map[uint16]struct{}, error)
}

type proxyUpstream struct {
	port  uint16
	https bool
}

type DomainTLSProxy struct {
	cb ProxyCallbacks

	tlsController *tlsutil.TLSController
	tproxy        *bpf.Tproxy

	probedHosts map[netip.Addr]proxyUpstream
}

func NewDomainTLSProxy(host *hclient.Client, cb ProxyCallbacks) (*DomainTLSProxy, error) {
	tlsController, err := tlsutil.NewTLSController(host)
	if err != nil {
		return nil, err
	}

	return &DomainTLSProxy{
		tlsController: tlsController,
		cb:            cb,

		probedHosts: make(map[netip.Addr]proxyUpstream),
	}, nil
}

func (p *DomainTLSProxy) Start(ip4, ip6 string, subnet4, subnet6 netip.Prefix, nfqueueNum, nfqueueGSONum uint16) error {
	err := p.tlsController.LoadRoot()
	if err != nil {
		return err
	}

	err = p.startQueue(nfqueueNum, 0)
	if err != nil {
		return fmt.Errorf("start queue: %w", err)
	}
	// declare GSO and partial checksum support to prevent reject from failing on macOS-originated packets (which are GSO + partial csum)
	// we only need GSO flag in ovm, and it breaks the docker bridge, so disable it in docker machine and enable it in ovm
	err = p.startQueue(nfqueueGSONum, nfqueue.NfQaCfgFlagGSO)
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

	httpProxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			err := p.rewriteRequest(r)
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

	go httpLn4.RunCallbackDispatcher(ln4, p.dispatchIncomingConn)
	go httpLn6.RunCallbackDispatcher(ln6, p.dispatchIncomingConn)

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

func (p *DomainTLSProxy) dispatchIncomingConn(conn net.Conn) (_ net.Conn, retErr error) {
	defer func() {
		if retErr != nil {
			conn.Close()
		}
	}()

	downstreamIP := conn.RemoteAddr().(*net.TCPAddr).IP
	is4 := downstreamIP.To4() != nil

	// don't try passthrough if request doesn't come from mac
	// in other words, only do reterm if request comes from mac
	if downstreamIP.Equal(SconHostBridgeIP4) || downstreamIP.Equal(SconHostBridgeIP6) {
		return conn, nil
	}

	destHost, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return nil, fmt.Errorf("split host/port: %w", err)
	}

	addr, upstream, err := p.cb.GetUpstreamByHost(destHost, is4)
	if err != nil {
		return nil, fmt.Errorf("get upstream: %w", err)
	}
	mark := p.cb.GetMark(upstream)

	var dialer *net.Dialer
	if !downstreamIP.Equal(upstream.IP) {
		dialer = dialerForTransparentBind(downstreamIP, mark)
	} else {
		dialer = &net.Dialer{}
	}

	proxyUp, err := p.getOrProbeHost(dialer, addr, upstream)
	if err != nil {
		return nil, err
	}

	// if it's not https, we can't do tls passthrough anyways
	if !proxyUp.https {
		return conn, nil
	}

	host := upstream.IP.String()
	port := strconv.Itoa(int(proxyUp.port))
	ctx, cancel := context.WithTimeout(context.Background(), httpsDialTimeout)
	defer cancel()
	passthroughConn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		// if we can't dial, we wouldn't be able to reterm anyways, so just error
		return nil, fmt.Errorf("dial upstream: %w", err)
	}

	defer passthroughConn.Close()
	defer conn.Close()
	tcppump.Pump2SpTcpTcp(conn.(*net.TCPConn), passthroughConn.(*net.TCPConn))

	// don't let this connection be submitted to http server
	return nil, nil
}

func (p *DomainTLSProxy) rewriteRequest(r *httputil.ProxyRequest) error {
	headerHost := r.In.Host         // the host that's passed in the host header
	connHost := r.In.TLS.ServerName // the host that's used for upstream connection

	// downstream = client
	downstreamAddrStr, _, err := net.SplitHostPort(r.In.RemoteAddr)
	if err != nil {
		return err
	}
	downstreamIP := net.ParseIP(downstreamAddrStr)
	if downstreamIP == nil {
		return fmt.Errorf("parse ip: %s", downstreamAddrStr)
	}
	is4 := downstreamIP.To4() != nil

	addr, upstream, err := p.cb.GetUpstreamByHost(connHost, is4)
	if err != nil {
		return fmt.Errorf("get upstream: %w", err)
	}
	mark := p.cb.GetMark(upstream)

	var dialer *net.Dialer
	if !downstreamIP.Equal(upstream.IP) {
		dialer = dialerForTransparentBind(downstreamIP, mark)
	} else {
		dialer = &net.Dialer{}
	}

	proxyUp, err := p.getOrProbeHost(dialer, addr, upstream)
	if err != nil {
		return fmt.Errorf("get or probe host: %w", err)
	}

	scheme := "http"
	if proxyUp.https {
		scheme = "https"
	}

	r.SetURL(&url.URL{
		Scheme: scheme,
		// Host is mandatory
		// always use SNI for upstream, so we can pass through any Host header
		Host: headerHost,
		// SetURL takes *base* path
		Path: "/",
	})

	r.Out.Host = headerHost
	r.Out.Header["Forwarded"] = r.In.Header["Forwarded"]
	r.Out.Header["X-Forwarded-For"] = r.In.Header["X-Forwarded-For"]
	r.SetXForwarded()

	// embed local addr and proxy upstream in context
	// proxy upstream is an optimization
	newContext := context.WithValue(r.Out.Context(), MdnsContextKeyDownstream, downstreamIP)
	newContext = context.WithValue(newContext, MdnsContextKeyProxyUpstream, proxyUp)
	newContext = context.WithValue(newContext, MdnsContextKeyUpstream, upstream)
	r.Out = r.Out.WithContext(newContext)

	return nil
}

func (p *DomainTLSProxy) dialUpstream(ctx context.Context, network, addr string) (net.Conn, error) {

	upstream, ok := ctx.Value(MdnsContextKeyUpstream).(domainproxytypes.Upstream)
	if !ok {
		return nil, fmt.Errorf("upstream not found in context")
	}

	downstreamIP := ctx.Value(MdnsContextKeyDownstream)
	// fall back to normal dialer
	// namely, this is used for hairpin, ie when a machine makes a request to its own domainproxy ip
	var dialer *net.Dialer
	if downstreamIP, ok := downstreamIP.(net.IP); ok && downstreamIP != nil && !downstreamIP.Equal(upstream.IP) {
		dialer = dialerForTransparentBind(downstreamIP, p.cb.GetMark(upstream))
	} else {
		dialer = &net.Dialer{}
	}

	proxyUp, ok := ctx.Value(MdnsContextKeyProxyUpstream).(proxyUpstream)
	if !ok {
		return nil, fmt.Errorf("proxy upstream not found in context")
	}
	port := strconv.Itoa(int(proxyUp.port))

	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(upstream.IP.String(), port))
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
