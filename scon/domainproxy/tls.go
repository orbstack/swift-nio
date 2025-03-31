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
	"syscall"
	"time"

	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/scon/util/portprober"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const httpsDialTimeout = 500 * time.Millisecond
const probeGraceTime = 500 * time.Millisecond
const probeTimeout = 60 * time.Second

type MdnsContextKey int

const (
	MdnsContextKeyDownstream MdnsContextKey = iota
	MdnsContextKeyConnData
)

var (
	sconHostBridgeIP4 net.IP
	sconHostBridgeIP6 net.IP
	nat64SourceIp4    net.IP
)

func init() {
	sconHostBridgeIP4 = net.ParseIP(netconf.SconHostBridgeIP4)
	sconHostBridgeIP6 = net.ParseIP(netconf.SconHostBridgeIP6)
	nat64SourceIp4 = net.ParseIP(netconf.NAT64SourceIP4)
}

type ProxyCallbacks interface {
	// takes name or IP string
	GetUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error)
	GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error)
	GetMark(upstream domainproxytypes.Upstream) int

	NftableName() string
	NfqueueMarkSkip(mark uint32) uint32

	GetHostOpenPorts(host domainproxytypes.Host) (map[uint16]struct{}, error)
}

type probedHost struct {
	HTTPSPort uint16
	HTTPPort  uint16
}

func (p *probedHost) HasPort() bool {
	return p.HTTPSPort != 0 || p.HTTPPort != 0
}

func (p *probedHost) PreferredIsHTTPS() bool {
	return p.HTTPSPort != 0
}

func (p *probedHost) PreferredPort() uint16 {
	if p.HTTPSPort != 0 {
		return p.HTTPSPort
	}
	return p.HTTPPort
}

type DomainTLSProxy struct {
	cb ProxyCallbacks

	tlsController *TLSController
	tproxy        *bpf.Tproxy

	probeMu     syncx.Mutex
	probedHosts map[netip.Addr]probedHost
	probeTasks  map[netip.Addr]*portprober.HostProbe
}

func NewDomainTLSProxy(host *hclient.Client, cb ProxyCallbacks) (*DomainTLSProxy, error) {
	tlsController, err := NewTLSController(host)
	if err != nil {
		return nil, err
	}

	return &DomainTLSProxy{
		tlsController: tlsController,
		cb:            cb,

		probedHosts: make(map[netip.Addr]probedHost),
		probeTasks:  make(map[netip.Addr]*portprober.HostProbe),
	}, nil
}

func (p *DomainTLSProxy) Start(ip4, ip6 string, subnet4, subnet6 netip.Prefix, nfqueueNum uint16, tproxy *bpf.Tproxy) error {
	err := p.startQueue(nfqueueNum)
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

	ln4, err := netx.ListenTransparent(context.TODO(), "tcp", net.JoinHostPort(ip4, "0"))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ln6, err := netx.ListenTransparent(context.TODO(), "tcp", net.JoinHostPort(ip6, "0"))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	ln4RawConn, err := ln4.(syscall.Conn).SyscallConn()
	if err != nil {
		return fmt.Errorf("get rawconn from listener: %w", err)
	}
	err = util.UseRawConn(ln4RawConn, func(fd int) error {
		return tproxy.SetSock4(0, uint64(fd))
	})
	if err != nil {
		return fmt.Errorf("set tproxy socket: %w", err)
	}

	ln6RawConn, err := ln6.(syscall.Conn).SyscallConn()
	if err != nil {
		return fmt.Errorf("get file from listener: %w", err)
	}
	err = util.UseRawConn(ln6RawConn, func(fd int) error {
		return tproxy.SetSock6(0, uint64(fd))
	})
	if err != nil {
		return fmt.Errorf("set tproxy socket: %w", err)
	}

	err = tproxy.AttachNetNsFromPath("/proc/thread-self/ns/net")
	if err != nil {
		return fmt.Errorf("attach tproxy to netns: %w", err)
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
				return p.tlsController.GetCertForHost(hlo.ServerName)
			},
		},

		// inject conn upstream info into context
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, MdnsContextKeyConnData, conn.(*tls.Conn).NetConn().(*util.DataConn[connData]).Data)
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
					retErr = fmt.Errorf("set opt 1: %w", err)
					return
				}

				// set SO_MARK to prevent TPROXY routing loop (since is also going to the dest IP)
				// also, this mark provides routing for the return path when we spoof source IP
				err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
				if err != nil {
					retErr = fmt.Errorf("set opt 2: %w", err)
					return
				}

				// set IP_BIND_ADDRESS_NO_PORT to not bind to port so that ports are tracked by 4-tuple, not 2-tuple
				err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BIND_ADDRESS_NO_PORT, 1)
				if err != nil {
					retErr = fmt.Errorf("set opt 3: %w", err)
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

type connData struct {
	Dialer           *net.Dialer
	UpstreamConnInfo upstreamConnInfo
}

type upstreamConnInfo struct {
	Upstream domainproxytypes.Upstream
	Probed   probedHost
}

func (p *DomainTLSProxy) dispatchIncomingConn(conn net.Conn) (_ net.Conn, retErr error) {
	defer func() {
		if retErr != nil {
			conn.Close()
		}
	}()

	downstreamIP := conn.RemoteAddr().(*net.TCPAddr).IP
	is4 := downstreamIP.To4() != nil

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

	p.probeMu.Lock()
	probed, ok := p.probedHosts[addr]
	p.probeMu.Unlock()
	if !ok {
		downstreamAddr, ok := netip.AddrFromSlice(downstreamIP)
		if !ok {
			return nil, fmt.Errorf("parse addr")
		}

		var err error
		if probed, err = p.probeHost(addr, downstreamAddr); err != nil {
			return nil, fmt.Errorf("probe host from dispatch: %w", err)
		}
	}

	if !probed.HasPort() {
		conn.Close()
		return nil, nil
	}

	data := connData{
		Dialer: dialer,
		UpstreamConnInfo: upstreamConnInfo{
			Upstream: upstream,
			Probed:   probed,
		},
	}
	newConn := util.NewDataConn(conn.(*net.TCPConn), data)

	return newConn, nil
}

func (p *DomainTLSProxy) rewriteRequest(r *httputil.ProxyRequest) error {
	headerHost := r.In.Host // the host that's passed in the host header

	data := r.In.Context().Value(MdnsContextKeyConnData).(connData)
	scheme := "http"
	if data.UpstreamConnInfo.Probed.PreferredIsHTTPS() {
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
	newContext := context.WithValue(r.Out.Context(), MdnsContextKeyConnData, data)
	r.Out = r.Out.WithContext(newContext)

	return nil
}

func (p *DomainTLSProxy) dialUpstream(ctx context.Context, network, addr string) (net.Conn, error) {
	data := ctx.Value(MdnsContextKeyConnData).(connData)

	host := data.UpstreamConnInfo.Upstream.IP.String()
	port := strconv.Itoa(int(data.UpstreamConnInfo.Probed.PreferredPort()))
	return data.Dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
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

	logrus.WithFields(logrus.Fields{
		"domainIP": domainIP,
		"table":    p.cb.NftableName(),
	}).Debug("deleting from probed set due to ECONNREFUSED")

	nftPrefix := "domainproxy4_probed"
	if domainIP.To4() == nil {
		nftPrefix = "domainproxy6_probed"
	}

	// remove from probed set
	err := nft.WithTable(nft.FamilyInet, p.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetDeleteByName(conn, table, nftPrefix+"_tls", nft.IP(domainIP))
	})
	// ENOENT = raced with another ECONNREFUSED to remove from set
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("delete from probed set: %w", err)
	}

	// remove http upstream
	err = nft.WithTable(nft.FamilyInet, p.cb.NftableName(), func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.MapDeleteByName(conn, table, nftPrefix+"_http_upstreams", nft.IP(domainIP))
	})
	if err != nil && !errors.Is(err, unix.ENOENT) {
		logrus.WithError(err).Error("failed to remove http upstream")
	}

	return nil
}
