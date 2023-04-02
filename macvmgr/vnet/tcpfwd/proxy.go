package tcpfwd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gvaddr"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/proxy"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/proxy/socks"
	"github.com/kdrag0n/macvirt/macvmgr/vzf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
)

const (
	proxyProtoHttp  = "http"
	proxyProtoHttps = "https"
	proxyProtoSocks = "socks5"
)

type ProxyManager struct {
	hostNatIP4 tcpip.Address
	hostNatIP6 tcpip.Address

	dialerMu    sync.Mutex
	dialerAll   proxy.ContextDialer
	dialerHttp  proxy.ContextDialer
	dialerHttps proxy.ContextDialer

	httpMu            sync.Mutex
	requiresHttpProxy bool
	httpRevProxy      *httpReverseProxy
}

type fullDuplexTlsConn struct {
	*tls.Conn
}

func (c *fullDuplexTlsConn) CloseRead() error {
	// not supported by tls
	return nil
}

func newProxyManager(hostNatIP4 tcpip.Address, hostNatIP6 tcpip.Address) *ProxyManager {
	return &ProxyManager{
		hostNatIP4: hostNatIP4,
		hostNatIP6: hostNatIP6,
	}
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

	// if override is set, use it
	overrideConfig := vmconfig.Get().NetworkProxy
	if overrideConfig != "" {
		u, err := url.Parse(overrideConfig)
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

		// we use this as our ONLY proxy if overridden
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
		// don't care about socks url
		return nil, nil
	}

	// 2. if HTTPS: use it for HTTPS (443) only
	var lastProxyUrl *url.URL
	if settings.HTTPSEnable {
		logrus.WithFields(logrus.Fields{
			"host": settings.HTTPSProxy,
			"port": settings.HTTPSPort,
		}).Info("using proxy: https")

		u := &url.URL{
			Scheme: "https",
			Host:   net.JoinHostPort(settings.HTTPSProxy, strconv.Itoa(settings.HTTPSPort)),
		}

		if settings.HTTPSUser != "" {
			u.User = url.UserPassword(settings.HTTPSUser, settings.HTTPSPassword)
		}

		proxyDialer, err := proxy.FromURL(u, nil)
		if err != nil {
			return nil, fmt.Errorf("create HTTPS proxy: %w", err)
		}

		p.dialerHttps = proxyDialer.(proxy.ContextDialer)
		// don't return - we might still have http left to do
		lastProxyUrl = u
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
	}
	return nil, nil
}

func (p *ProxyManager) Refresh() error {
	settings, err := vzf.SwextProxyGetSettings()
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
		p.httpRevProxy = newHttpReverseProxy(proxyUrl)
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
	}

	if dialer == nil {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}

		return conn.(*net.TCPConn), nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	// unwrap SOCKS connection
	if socksConn, ok := conn.(*socks.Conn); ok {
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
	if err != nil && errors.Is(err, unix.ECONNREFUSED) && altHostIP != "" {
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
