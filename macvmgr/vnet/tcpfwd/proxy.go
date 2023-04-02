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

	dialerMu sync.Mutex
	dialer   proxy.ContextDialer

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

func (p *ProxyManager) updateProxyDialer(settings *vzf.SwextProxySettings) (*url.URL, error) {
	p.dialerMu.Lock()
	defer p.dialerMu.Unlock()

	// if override is set, use it
	overrideConfig := vmconfig.Get().NetworkProxy
	if overrideConfig != "" {
		u, err := url.Parse(overrideConfig)
		if err != nil {
			return nil, err
		}
		logrus.WithFields(logrus.Fields{
			"host": u.Host,
			"port": u.Port(),
		}).Info("using proxy: override")

		proxyDialer, err := proxy.FromURL(u, nil)
		if err != nil {
			return nil, err
		}

		p.dialer = proxyDialer.(proxy.ContextDialer)
		// normalize socks5h -> socks5
		if u.Scheme == "socks5h" {
			u.Scheme = "socks5"
		}
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
			return nil, err
		}

		socksDialer := dialer.(*socks.Dialer)
		p.dialer = socksDialer
		// don't care about socks url
		return nil, nil
	}

	// 2. if HTTPS: use it for everything
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
			return nil, err
		}

		p.dialer = proxyDialer.(proxy.ContextDialer)
		return u, nil
	}

	// 3. if HTTP: use it for everything
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

		p.dialer = proxyDialer.(proxy.ContextDialer)
		return u, nil
	}

	// 4. if none: use direct connection
	logrus.Info("using proxy: none")
	p.dialer = nil
	return nil, nil
}

func (p *ProxyManager) Refresh() error {
	settings, err := vzf.SwextProxyGetSettings()
	if err != nil {
		return fmt.Errorf("get proxy settings: %w", err)
	}

	logrus.WithField("settings", settings).Debug("got proxy settings")

	proxyUrl, err := p.updateProxyDialer(settings)
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

func (p *ProxyManager) DialContextTCP(ctx context.Context, addr string, port int) (FullDuplexConn, error) {
	p.dialerMu.Lock()
	dialer := p.dialer
	p.dialerMu.Unlock()

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

	extConn, err := p.DialContextTCP(ctx, extAddr, extPort)
	if err != nil && errors.Is(err, unix.ECONNREFUSED) && altHostIP != "" {
		// try the other host IP
		// do not set localAddress or icmp unreachable logic below will send wrong protocol
		logrus.Debugf("TCP forward [%v] dial retry host", extAddr)
		extAddr = net.JoinHostPort(altHostIP.String(), strconv.Itoa(int(extPort)))
		// don't reset timeout - localhost shouldn't take so long
		// if it did, it was probably listen backlog full, so we don't want to retry too long
		extConn, err = p.DialContextTCP(ctx, extAddr, extPort)
	}

	if err != nil {
		return nil, extAddr, err
	}

	// at this point we can unwrap the proxy's tcp conn
	// use this, not interface cast, for tcp copy hotpath in pump2 (avoid virtual call)
	return extConn, extAddr, nil
}
