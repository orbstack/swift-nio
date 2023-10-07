package tcpfwd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vnet/gvaddr"
	"github.com/orbstack/macvirt/vmgr/vnet/proxy"
	"github.com/orbstack/macvirt/vmgr/vnet/proxy/socks"
	dnssrv "github.com/orbstack/macvirt/vmgr/vnet/services/dns"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
)

const (
	proxyProtoHttp  = "http"
	proxyProtoHttps = "https"
	proxyProtoSocks = "socks5"
)

const settingsRefreshDebounce = 100 * time.Millisecond

type ProxyManager struct {
	hostNatIP4 tcpip.Address
	hostNatIP6 tcpip.Address

	dialerMu      sync.Mutex
	dialerAll     proxy.ContextDialer
	dialerHttp    proxy.ContextDialer
	dialerHttps   proxy.ContextDialer
	perHostFilter *proxy.PerHost

	httpMu            sync.Mutex
	requiresHttpProxy bool
	httpRevProxy      *httpReverseProxy

	vmconfigCh chan vmconfig.VmConfigChange
	stopCh     chan struct{}
	// for drm: the proxy url used by HTTPS connection
	httpsProxyUrl *url.URL

	DnsServer *dnssrv.DnsServer
}

type ProxyDialError struct {
	Err error
}

func (e *ProxyDialError) Error() string {
	return fmt.Sprintf("via proxy: %v", e.Err)
}

func (e *ProxyDialError) Unwrap() error {
	return e.Err
}

type fullDuplexTlsConn struct {
	*tls.Conn
}

func (c *fullDuplexTlsConn) CloseRead() error {
	// not supported by tls
	return nil
}

func newProxyManager(hostNatIP4 tcpip.Address, hostNatIP6 tcpip.Address) *ProxyManager {
	mgr := &ProxyManager{
		hostNatIP4: hostNatIP4,
		hostNatIP6: hostNatIP6,
		stopCh:     make(chan struct{}),
	}

	// subscribe to vmconfig
	mgr.vmconfigCh = vmconfig.SubscribeDiff()
	go mgr.monitorChanges()

	return mgr
}

func (p *ProxyManager) monitorChanges() {
	// note: refresh can block because of keychain prompt
	sysRefreshDebounce := syncx.NewFuncDebounce(settingsRefreshDebounce, func() {
		err := p.Refresh()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh proxy settings (sys settings change)")
		}
	})

	for {
		select {
		case patch := <-p.vmconfigCh:
			if patch.New.NetworkProxy != patch.Old.NetworkProxy {
				err := p.Refresh()
				if err != nil {
					logrus.WithError(err).Error("failed to refresh proxy settings (config change)")
				}
			}
		case <-vzf.SwextProxyChangesChan:
			sysRefreshDebounce.Call()
		case <-p.stopCh:
			return
		}
	}
}

func (p *ProxyManager) excludeProxyHost(hostPort string) error {
	if p.perHostFilter == nil {
		return nil
	}

	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		if addrError, ok := err.(*net.AddrError); ok && addrError.Err == "missing port in address" {
			host = hostPort
		} else {
			return err
		}
	}

	logrus.WithField("host", host).Debug("adding host to proxy exclusion list")
	p.perHostFilter.AddFromString(host)

	return nil
}

// general idea:
// SOCKS: use it for everything
// HTTP: use it for HTTP *and* HTTPS
// HTTPS: use it for HTTP *and* HTTPS
// determined by ports 80 and 443
// because proxies tend to block other ports
func (p *ProxyManager) updateDialers(settings *vzf.SwextProxySettings) (*url.URL, error) {
	p.dialerMu.Lock()
	defer p.dialerMu.Unlock()

	// reset all proxies
	p.dialerAll = nil
	p.dialerHttp = nil
	p.dialerHttps = nil
	p.httpsProxyUrl = nil

	// build exceptions list
	p.perHostFilter = proxy.NewPerHost()
	for _, item := range settings.ExceptionsList {
		// some people stuff everything into one entry with \n?
		// split it up
		for _, host := range strings.Fields(item) {
			if host == "" {
				continue
			}

			logrus.WithField("host", host).Debug("adding host to proxy exclusion list")
			p.perHostFilter.AddFromString(host)
		}
	}

	// pick behavior based on config
	configVal := vmconfig.Get().NetworkProxy
	switch configVal {
	case vmconfig.ProxyNone:
		logrus.Info("using proxy: none (override)")
		// discard the per-host filter to save memory
		p.perHostFilter = nil
		return nil, nil
	case vmconfig.ProxyAuto:
		break
	default:
		u, err := url.Parse(configVal)
		if err != nil {
			return nil, err
		}
		logrus.WithFields(logrus.Fields{
			"scheme": u.Scheme,
			"host":   u.Host,
			"port":   u.Port(),
		}).Info("using proxy: override")

		proxyDialer, err := proxy.FromURL(u, nil)
		if err != nil {
			return nil, err
		}

		// normalize socks5h -> socks5
		if u.Scheme == "socks5h" {
			u.Scheme = "socks5"
		}

		// overrides are a bit special, because there's only one setting
		// if SOCKS: use it for everything
		// if HTTP: use it for HTTP *and* HTTPS
		// if HTTPS: use it for HTTP *and* HTTPS
		ctxDialer := proxyDialer.(proxy.ContextDialer)
		switch u.Scheme {
		case proxyProtoSocks:
			p.dialerAll = ctxDialer
			p.dialerHttp = ctxDialer
			p.dialerHttps = ctxDialer
		case proxyProtoHttp, proxyProtoHttps:
			p.dialerAll = nil
			p.dialerHttp = ctxDialer
			p.dialerHttps = ctxDialer
		default:
			return nil, errors.New("invalid proxy scheme: " + u.Scheme)
		}

		// exclude the proxy from filter to prevent infinite loop
		err = p.excludeProxyHost(u.Host)
		if err != nil {
			logrus.WithError(err).Error("failed to exclude proxy host from filter")
		}

		// we use this as our ONLY proxy if overridden
		p.httpsProxyUrl = u
		return u, nil
	}

	// 1. if SOCKS: use it for everything
	if settings.SOCKSEnable {
		logrus.WithFields(logrus.Fields{
			"host": settings.SOCKSProxy,
			"port": settings.SOCKSPort,
		}).Info("using proxy: socks")

		var auth proxy.Auth
		if settings.SOCKSUser != "" {
			auth = proxy.Auth{
				User:     settings.SOCKSUser,
				Password: settings.SOCKSPassword,
			}
		}

		dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(settings.SOCKSProxy, strconv.Itoa(settings.SOCKSPort)), &auth, nil)
		if err != nil {
			return nil, fmt.Errorf("create SOCKS proxy: %w", err)
		}

		ctxDialer := dialer.(proxy.ContextDialer)
		p.dialerAll = ctxDialer
		p.dialerHttp = ctxDialer
		p.dialerHttps = ctxDialer

		// make an url for drm proxy
		u := &url.URL{
			Scheme: "socks5",
			Host:   net.JoinHostPort(settings.SOCKSProxy, strconv.Itoa(settings.SOCKSPort)),
		}
		if settings.SOCKSUser != "" {
			u.User = url.UserPassword(settings.SOCKSUser, settings.SOCKSPassword)
		}

		// exclude the proxy from filter to prevent infinite loop
		err = p.excludeProxyHost(u.Host)
		if err != nil {
			logrus.WithError(err).Error("failed to exclude proxy host from filter")
		}

		p.httpsProxyUrl = u
		return u, nil
	}

	// 2. if HTTPS: use it for HTTPS (443) only
	var lastProxyUrl *url.URL
	if settings.HTTPSEnable {
		logrus.WithFields(logrus.Fields{
			"host": settings.HTTPSProxy,
			"port": settings.HTTPSPort,
		}).Info("using proxy: https")

		// this is a proxy *for* HTTPS,
		// but the proxy *protocol* is HTTP
		u := &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(settings.HTTPSProxy, strconv.Itoa(settings.HTTPSPort)),
		}

		if settings.HTTPSUser != "" {
			u.User = url.UserPassword(settings.HTTPSUser, settings.HTTPSPassword)
		}

		proxyDialer, err := proxy.FromURL(u, nil)
		if err != nil {
			return nil, fmt.Errorf("create HTTPS proxy: %w", err)
		}

		// exclude the proxy from filter to prevent infinite loop
		err = p.excludeProxyHost(u.Host)
		if err != nil {
			logrus.WithError(err).Error("failed to exclude proxy host from filter")
		}

		p.dialerHttps = proxyDialer.(proxy.ContextDialer)
		// don't return - we might still have http left to do
		lastProxyUrl = u
		p.httpsProxyUrl = u
	}

	// 3. if HTTP: use it for HTTP (80) only
	if settings.HTTPEnable {
		logrus.WithFields(logrus.Fields{
			"host": settings.HTTPProxy,
			"port": settings.HTTPPort,
		}).Info("using proxy: http")

		u := &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(settings.HTTPProxy, strconv.Itoa(settings.HTTPPort)),
		}

		if settings.HTTPUser != "" {
			u.User = url.UserPassword(settings.HTTPUser, settings.HTTPPassword)
		}

		proxyDialer, err := proxy.FromURL(u, nil)
		if err != nil {
			return nil, err
		}

		// exclude the proxy from filter to prevent infinite loop
		err = p.excludeProxyHost(u.Host)
		if err != nil {
			logrus.WithError(err).Warn("failed to exclude proxy host from filter")
		}

		p.dialerHttp = proxyDialer.(proxy.ContextDialer)
		lastProxyUrl = u
	}

	// if we have a last proxy url, return it
	// this means either http, https, or both are set
	if lastProxyUrl != nil {
		return lastProxyUrl, nil
	}

	// 4. if none: use direct connection
	if p.dialerAll == nil && p.dialerHttp == nil && p.dialerHttps == nil {
		logrus.Info("using proxy: none")
		// discard the per-host filter to save memory
		p.perHostFilter = nil
	}
	return nil, nil
}

func (p *ProxyManager) Refresh() error {
	// don't read from keychain if not needed
	// it can trigger keychain permission prompt
	needAuth := vmconfig.Get().NetworkProxy == vmconfig.ProxyAuto
	settings, err := vzf.SwextProxyGetSettings(needAuth)
	if err != nil {
		return fmt.Errorf("get proxy settings: %w", err)
	}

	logrus.WithField("settings", settings).Debug("got proxy settings")

	proxyUrl, err := p.updateDialers(settings)
	if err != nil {
		return err
	}

	// check if we need to use HTTP proxy
	p.httpMu.Lock()
	requiresHttp := proxyUrl != nil && (proxyUrl.Scheme == proxyProtoHttp || proxyUrl.Scheme == proxyProtoHttps)
	if requiresHttp {
		p.dialerMu.Lock()
		defer p.dialerMu.Unlock()

		oldHttpRevProxy := p.httpRevProxy
		p.httpRevProxy = newHttpReverseProxy(proxyUrl, p.perHostFilter, p)
		if oldHttpRevProxy != nil {
			oldHttpRevProxy.Close()
		}
	}
	p.requiresHttpProxy = requiresHttp
	p.httpMu.Unlock()

	return nil
}

func (p *ProxyManager) dialContextTCPInternal(ctx context.Context, addr string, port int, tcpipAddr tcpip.Address) (FullDuplexConn, error) {
	var dialer proxy.ContextDialer
	// skip everything if not eligible for proxying
	if p.isProxyEligibleIPPost(tcpipAddr) {
		p.dialerMu.Lock()
		switch port {
		case 80:
			dialer = p.dialerHttp
		case 443:
			dialer = p.dialerHttps
		default:
			dialer = p.dialerAll
		}
		p.dialerMu.Unlock()

		// if we got a proxy, check if we should bypass it
		// for perf, we skip this logic if no proxy
		if dialer != nil {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			if p.perHostFilter != nil && p.perHostFilter.TestBypass(host, p.DnsServer) {
				// bypass
				logrus.Debugf("bypassing proxy for %s (dial)", host)
				dialer = nil
			}
		}
	}

	if dialer == nil {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}

		netx.SetLongKeepalive(conn)
		return conn.(*net.TCPConn), nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &ProxyDialError{err}
	}
	netx.SetLongKeepalive(conn)

	// unwrap SOCKS connection
	if socksConn, ok := conn.(*socks.Conn); ok {
		netx.SetLongKeepalive(socksConn.TCPConn)
		return socksConn.TCPConn, nil
	}

	// unwrap TLS connection
	if tlsConn, ok := conn.(*tls.Conn); ok {
		return &fullDuplexTlsConn{tlsConn}, nil
	}

	// http and direct
	return conn.(*net.TCPConn), nil
}

func (p *ProxyManager) DialForward(localAddress tcpip.Address, extPort int) (FullDuplexConn, string, error) {
	// host NAT: try dial preferred v4/v6 first, then fall back to the other one
	var altHostIP tcpip.Address
	if localAddress == p.hostNatIP4 {
		localAddress = gvaddr.LoopbackGvIP4
		altHostIP = gvaddr.LoopbackGvIP6
	} else if localAddress == p.hostNatIP6 {
		localAddress = gvaddr.LoopbackGvIP6
		altHostIP = gvaddr.LoopbackGvIP4
	}
	extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(extPort))

	ctx, cancel := context.WithTimeout(context.TODO(), tcpConnectTimeout)
	defer cancel()

	extConn, err := p.dialContextTCPInternal(ctx, extAddr, extPort, localAddress)
	if err != nil && errors.Is(err, unix.ECONNREFUSED) && altHostIP != (tcpip.Address{}) {
		// try the other host IP
		// do not set localAddress or icmp unreachable logic below will send wrong protocol
		logrus.Debugf("TCP forward [%v] dial retry host", extAddr)
		extAddr = net.JoinHostPort(altHostIP.String(), strconv.Itoa(int(extPort)))
		// don't reset timeout - localhost shouldn't take so long
		// if it did, it was probably listen backlog full, so we don't want to retry too long
		extConn, err = p.dialContextTCPInternal(ctx, extAddr, extPort, localAddress)
	}

	if err != nil {
		return nil, extAddr, err
	}

	// at this point we can unwrap the proxy's tcp conn
	// use this, not interface cast, for tcp copy hotpath in pump2 (avoid virtual call)
	return extConn, extAddr, nil
}

func (p *ProxyManager) isProxyEligibleIPPre(addr tcpip.Address) bool {
	// never proxy host nat (before translation)
	return addr != p.hostNatIP4 && addr != p.hostNatIP6
}

func (p *ProxyManager) isProxyEligibleIPPost(addr tcpip.Address) bool {
	// never proxy host nat (the translated versions)
	return addr != gvaddr.LoopbackGvIP4 && addr != gvaddr.LoopbackGvIP6
}

func (p *ProxyManager) GetHTTPSProxyURL() *url.URL {
	p.dialerMu.Lock()
	defer p.dialerMu.Unlock()

	return p.httpsProxyUrl
}

func (p *ProxyManager) Close() error {
	p.httpMu.Lock()
	defer p.httpMu.Unlock()

	if p.httpRevProxy != nil {
		p.httpRevProxy.Close()
		p.httpRevProxy = nil
	}

	// close OK: used to signal select loop
	close(p.stopCh)
	vmconfig.UnsubscribeDiff(p.vmconfigCh)

	return nil
}
