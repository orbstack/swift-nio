package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/orbstack/macvirt/scon/agent/tlsutil"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// in case of extremely high load
	// limited to prevent mem leak in case of wildcard scanning
	hostDialLRUSize = 250
)

type tlsProxy struct {
	hostDialLRU *lru.Cache[string, hostDialInfo]

	controller *tlsutil.TLSController
}

type hostDialInfo struct {
	bindAddr *net.TCPAddr
	// *net.TCPAddr would be nice, but Dialer has no context-aware DialTCP
	// https://github.com/golang/go/issues/49097
	dialAddr string
}

func newTLSProxy(host *hclient.Client) (*tlsProxy, error) {
	hostDialLRU, err := lru.New[string, hostDialInfo](hostDialLRUSize)
	if err != nil {
		return nil, err
	}

	tlsController, err := tlsutil.NewTLSController(host)
	if err != nil {
		return nil, err
	}

	return &tlsProxy{
		hostDialLRU: hostDialLRU,
		controller:  tlsController,
	}, nil
}

func tcpAddrToSockaddr(a *net.TCPAddr) unix.Sockaddr {
	// we use a zero port because
	// - binding to the same 2-tuple as client (getpeername) fails with EADDRINUSE
	// - even if we pick an ephemeral port that conflicts with one that macOS is using, it's ok, because the 5-tuple is different (different dest port: 80 vs. 443) and Linux conntrack only requires unique 5-tuple. therefore no conflict is possible
	//   - especially because macOS doesn't reuse same src ports
	// - src port doesn't matter for HTTPS or even websocket
	if ip4 := a.IP.To4(); ip4 != nil {
		sa := &unix.SockaddrInet4{Port: 0}
		copy(sa.Addr[:], ip4)
		return sa
	} else {
		sa := &unix.SockaddrInet6{Port: 0}
		copy(sa.Addr[:], a.IP)
		return sa
	}
}

func (t *tlsProxy) dispatchConn(conn net.Conn) (bool, error) {
	// check original destination port (we have this info b/c IP_TRANSPARENT)
	destPort := conn.LocalAddr().(*net.TCPAddr).Port

	// always attempt to make a direct connection and dial orig port on orig IP first
	// since these are local containers, connection should fail fast and return RST (-ECONNREFUSED)
	// EXCEPT: if dest is a machine and user installed ufw, then ufw will drop the packet and we'll get a timeout
	//   * workaround: short connection timeout
	//     * this works: if load test is causing listen backlog to be full, we will get immediate RST because port is open in firewall
	// still need to bind to host to get correct cfwd behavior, especially for 443->8443 or 443->https_port case
	// TODO: how can we do this in kernel, without userspace proxying? is SOCKMAP good?
	dialer := dialerForTransparentBind(conn.RemoteAddr().(*net.TCPAddr))
	dialer.Timeout = 500 * time.Millisecond
	upstreamConn, err := dialer.DialContext(context.Background(), "tcp", conn.LocalAddr().String())
	if err == nil {
		// connection succeeded. proxy it
		defer upstreamConn.Close()
		defer conn.Close()
		tcppump.Pump2SpTcpTcp(conn.(*net.TCPConn), upstreamConn.(*net.TCPConn))
		return false, nil
	}

	switch destPort {
	case 22:
		// SSH
		// TODO: redirect to machine
		return false, nil // should never get here: not yet implemented in iptables
	case 80:
		// HTTP
		// TODO: userspace HTTP probing
		return false, nil // should never get here: not yet implemented in iptables
	case 443:
		// continue and pass this to HTTPS TLS proxy
		return true, nil
	default:
		// unknown port. should never get here unless iptables rules are broken
		return false, fmt.Errorf("unknown dest port: %d", destPort)
	}
}

func (t *tlsProxy) Start() error {
	err := t.controller.LoadRoot()
	if err != nil {
		return err
	}

	lcfg := net.ListenConfig{
		// set IP_TRANSPARENT to accept TPROXY connections
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			c.Control(func(fd uintptr) {
				// Go sets SO_REUSEADDR by default
				err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TRANSPARENT, 1)
			})
			return err
		},
	}

	ln4, err := lcfg.Listen(context.TODO(), "tcp", net.JoinHostPort(netconf.VnetTlsProxyIP4, ports.DockerMachineTlsProxyStr))
	if err != nil {
		return err
	}

	ln6, err := lcfg.Listen(context.TODO(), "tcp", net.JoinHostPort(netconf.VnetTlsProxyIP6, ports.DockerMachineTlsProxyStr))
	if err != nil {
		return err
	}

	// wrap with dispatcher callback
	ln4dispatch := NewDispatchedListener(ln4, t.dispatchConn)
	go ln4dispatch.Run()
	ln6dispatch := NewDispatchedListener(ln6, t.dispatchConn)
	go ln6dispatch.Run()

	// we *could* do a raw TLS proxy, but I did it this way for flexibility.
	// in the future we could offer request-capturing with this method
	// this is more complicated but that's really not an impossible feature
	// another slightly-nice thing is that this provides HTTP/2
	// SNI proxy could also result in unexpected CORS values (https:// instead of http://) so this is needed for correctness
	httpProxy := &httputil.ReverseProxy{
		// TODO: consider FlushInterval=100ms to avoid streaming side effects
		Rewrite: func(r *httputil.ProxyRequest) {
			err := t.rewriteRequest(r)
			if err != nil {
				logrus.WithError(err).Error("failed to rewrite request")
			}
		},
		Transport: &http.Transport{
			DialContext: t.dialUpstream,
			// establishing conns is cheap locally
			// do not limit MaxConnsPerHost in case of load testing
			IdleConnTimeout: 5 * time.Second,
			// allow more idle conns to avoid TIME_WAIT hogging all ports under load test (default 2)
			// otherwise we get "connect: cannot assign requested address" after too long
			MaxIdleConnsPerHost: 200,
		},
	}

	server := &http.Server{
		Handler: httpProxy,
		TLSConfig: &tls.Config{
			GetCertificate: func(hlo *tls.ClientHelloInfo) (*tls.Certificate, error) {
				// for security, only allow .local SNIs
				// no need to check mdns registry (which is expensive RPC) because any .local domain containers are allowed to register could be registered by user anyway
				if !strings.HasSuffix(hlo.ServerName, ".local") {
					return nil, nil
				}

				return t.controller.MakeCertForHost(hlo.ServerName)
			},
		},
	}

	go func() {
		// we use TLSConfig.GetCertificate instead
		err := server.ServeTLS(ln4dispatch, "", "")
		if err != nil {
			logrus.WithError(err).Error("tls proxy server failed")
		}
	}()

	go func() {
		// we use TLSConfig.GetCertificate instead
		err := server.ServeTLS(ln6dispatch, "", "")
		if err != nil {
			logrus.WithError(err).Error("tls proxy server failed")
		}
	}()

	return nil
}

func (t *tlsProxy) rewriteRequest(r *httputil.ProxyRequest) error {
	// use SNI if Host is missing
	host := r.In.Host
	if host == "" {
		host = r.In.TLS.ServerName
	}

	// passthrough URL to host
	r.SetURL(&url.URL{
		Scheme: "http",
		// Host is mandatory. we don't have access to SNI here
		Host: host,
		// this is *base* path
		Path: "/",
	})
	r.Out.Host = host

	// let's record the target IP
	// client thinks each, so this is OK
	// if we ever change this and allow connecting through proxy without, we'll have to revisit this and query from dns server instead
	// but until then, I believe that this is more reliable. for example, it preserves ipv4 vs. v6 intention (but that could also be done with dns A vs. AAAA)
	// localAddr = getsockname(), which is connection dest addr due to TPROXY
	localAddr := r.In.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)

	// also record the last requested dial IP (probably host bridge IP) for this addr
	// always accurate because macOS only has 1 IP per interface, and containers are only reachable from 1 interface
	remoteAddrStr, remotePortStr, err := net.SplitHostPort(r.In.RemoteAddr)
	if err != nil {
		return err
	}
	remoteAddr := net.ParseIP(remoteAddrStr)
	if remoteAddr == nil {
		return fmt.Errorf("invalid remote addr: %s", remoteAddrStr)
	}
	remotePort, err := strconv.Atoi(remotePortStr)
	if err != nil {
		return err
	}

	// port is always 80 because we proxy to plaintext HTTP upstream
	// this port 80 relies on bpf cfwd port scanning
	localAddr.Port = 80

	// save all the IP info
	t.hostDialLRU.Add(host, hostDialInfo{
		// looks reversed but this is correct due to proxy/server roles
		bindAddr: &net.TCPAddr{
			IP:   remoteAddr,
			Port: remotePort,
		},
		dialAddr: localAddr.String(),
	})

	return nil
}

func dialerForTransparentBind(bindAddr *net.TCPAddr) *net.Dialer {
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
				err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, TlsProxyUpstreamMark)
				if err != nil {
					retErr = fmt.Errorf("failed to set opt 2: %w", err)
					return
				}

				// bind to local address, spoof source IP of client (but not port)
				err = unix.Bind(int(fd), tcpAddrToSockaddr(bindAddr))
				if err != nil {
					// just a warning. continue but with wrong src IP
					logrus.WithError(err).Warn("failed to bind to laddr")
				}
			})
			if err2 != nil {
				return err2
			}
			return retErr
		},
	}
}

func (t *tlsProxy) dialUpstream(ctx context.Context, network, addr string) (net.Conn, error) {
	dialHost, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	// resolve dial info from request's host
	dialInfo, ok := t.hostDialLRU.Get(dialHost)
	if !ok {
		return nil, fmt.Errorf("no dial info for host: %s", dialHost)
	}

	// dial the host
	// need to use Dialer for context awareness, then implement bind and Control ourselves
	dialer := dialerForTransparentBind(dialInfo.bindAddr)

	logrus.WithField("bindAddr", dialInfo.bindAddr).WithField("dialAddr", dialInfo.dialAddr).Trace("dialing upstream")
	return dialer.DialContext(ctx, "tcp", dialInfo.dialAddr)
}
