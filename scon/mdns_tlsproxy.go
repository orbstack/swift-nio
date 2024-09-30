package main

import (
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

	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/tlsutil"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type mdnsContextKey int

const (
	mdnsContextKeyDownstreamIp = iota
)

type tlsProxy struct {
	tlsController *tlsutil.TLSController
	registry      *mdnsRegistry
	tproxy        *bpf.Tproxy
}

func newTlsProxy(host *hclient.Client, registry *mdnsRegistry) (*tlsProxy, error) {
	tlsController, err := tlsutil.NewTLSController(host)
	if err != nil {
		return nil, fmt.Errorf("failed to create tls controller: %w", err)
	}

	return &tlsProxy{
		tlsController: tlsController,
		registry:      registry,
	}, nil
}

func (t *tlsProxy) Start() error {
	err := t.tlsController.LoadRoot()
	if err != nil {
		return err
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}
	loTlsProxyAddr4, err := netlink.ParseAddr(netconf.VnetTlsProxyIP4 + "/32")
	err = netlink.AddrAdd(lo, loTlsProxyAddr4)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	loTlsProxyAddr6, err := netlink.ParseAddr(netconf.VnetTlsProxyIP6 + "/128")
	err = netlink.AddrAdd(lo, loTlsProxyAddr6)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}

	ln4, err := net.Listen("tcp", net.JoinHostPort(netconf.VnetTlsProxyIP4, "0"))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	ln6, err := net.Listen("tcp", net.JoinHostPort(netconf.VnetTlsProxyIP6, "0"))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	dispatchedLn4 := util.NewDispatchedListener(ln4, t.dispatchIncomingConn)
	go dispatchedLn4.Run()
	dispatchedLn6 := util.NewDispatchedListener(ln6, t.dispatchIncomingConn)
	go dispatchedLn6.Run()

	httpProxy := &httputil.ReverseProxy{
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

	httpServer := &http.Server{
		Handler: httpProxy,
		TLSConfig: &tls.Config{
			GetCertificate: func(hlo *tls.ClientHelloInfo) (*tls.Certificate, error) {
				if !strings.HasSuffix(hlo.ServerName, ".local") {
					return nil, nil
				}

				return t.tlsController.MakeCertForHost(hlo.ServerName)
			},
		},
	}

	go func() {
		err := httpServer.ServeTLS(dispatchedLn4, "", "")
		if err != nil {
			logrus.WithError(err).Error("mdns tlsproxy: serve tls failed")
		}
	}()
	go func() {
		err := httpServer.ServeTLS(dispatchedLn6, "", "")
		if err != nil {
			logrus.WithError(err).Error("mdns tlsproxy: serve tls failed")
		}
	}()

	tproxy, err := bpf.NewTproxy(domainproxySubnet4Prefix, domainproxySubnet6Prefix, 443)
	if err != nil {
		return fmt.Errorf("mdns tlsproxy: failed to create tproxy bpf: %w", err)
	}

	ln4File, err := ln4.(*net.TCPListener).File()
	if err != nil {
		return fmt.Errorf("failed to get file from listener: %w", err)
	}
	err = tproxy.SetSock4(uint64(ln4File.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set tproxy socket: %w", err)
	}

	ln6File, err := ln6.(*net.TCPListener).File()
	if err != nil {
		return fmt.Errorf("failed to get file from listener: %w", err)
	}
	err = tproxy.SetSock6(uint64(ln6File.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set tproxy socket: %w", err)
	}

	t.tproxy = tproxy

	logrus.Debug("mdns tls proxy started")
	return nil
}

func dialerForTransparentBind(bindIp net.IP, mark int) *net.Dialer {
	var sa unix.Sockaddr
	if ip4 := bindIp.To4(); ip4 != nil {
		sa4 := &unix.SockaddrInet4{Port: 0}
		copy(sa4.Addr[:], ip4)
		sa = sa4
	} else {
		sa6 := &unix.SockaddrInet6{Port: 0}
		copy(sa6.Addr[:], bindIp)
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
					logrus.WithError(err).WithFields(logrus.Fields{"ip": bindIp, "sa": sa}).Warn("failed to bind to laddr")
				}
			})
			if err2 != nil {
				return err2
			}
			return retErr
		},
	}
}

func (t *tlsProxy) getMark(proxyAddr netip.Addr) int {
	mark := netconf.VmFwmarkTproxyOutboundBit
	if _, has := t.registry.domainproxy.dockerSet[proxyAddr]; has {
		mark |= netconf.VmFwmarkDockerRouteBit
	}

	return mark
}

// returns proxy addr, upstream ip, error
func (t *tlsProxy) getUpstream(host string, v4 bool) (netip.Addr, net.IP, error) {
	proxyAddr := netip.Addr{}
	if proxyAddrVal, err := netip.ParseAddr(host); err == nil {
		proxyAddr = proxyAddrVal
	} else {
		proxyIp4, proxyIp6 := t.registry.getIpsForName(strings.TrimSuffix(host, ".") + ".")

		if v4 && proxyIp4 != nil {
			if proxyAddr4, ok := netip.AddrFromSlice(proxyIp4); ok {
				proxyAddr = proxyAddr4
			}
		}
		if !v4 && proxyIp6 != nil {
			if proxyAddr6, ok := netip.AddrFromSlice(proxyIp6); ok {
				proxyAddr = proxyAddr6
			}
		}
	}

	if proxyAddr == (netip.Addr{}) {
		return netip.Addr{}, nil, errors.New("could not find proxyaddr")
	}

	upstreamIp, has := t.registry.domainproxy.ipMap[proxyAddr]
	if !has {
		return netip.Addr{}, nil, errors.New("could not find backend in mdns registry")
	}

	return proxyAddr, upstreamIp, nil
}

func (t *tlsProxy) dispatchIncomingConn(conn net.Conn) (bool, error) {
	// this function just lets us directly passthrough to an existing ssl server. this should be removed soon in favor of port probing

	downstreamIp := conn.RemoteAddr().(*net.TCPAddr).IP
	is4 := downstreamIp.To4() != nil

	// this works because we never change the dest, only hook sk_assign (like tproxy but sillier)
	destHost, destPort, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return false, fmt.Errorf("couldn't split %s into host and port", conn.LocalAddr().String())
	}

	proxyAddr, upstreamIp, err := t.getUpstream(destHost, is4)
	if err != nil {
		return false, err
	}
	mark := t.getMark(proxyAddr)

	// always attempt to make a direct connection and dial orig port on orig IP first
	// since these are local containers, connection should fail fast and return RST (-ECONNREFUSED)
	// EXCEPT: if dest is a machine and user installed ufw, then ufw will drop the packet and we'll get a timeout
	//   * workaround: short connection timeout
	//     * this works: if load test is causing listen backlog to be full, we will get immediate RST because port is open in firewall
	// still need to bind to host to get correct cfwd behavior, especially for 443->8443 or 443->https_port case
	// TODO: how can we do this in kernel, without userspace proxying? is SOCKMAP good?
	dialer := dialerForTransparentBind(downstreamIp, mark)
	dialer.Timeout = 500 * time.Millisecond
	upstreamConn, err := dialer.DialContext(context.Background(), "tcp", net.JoinHostPort(upstreamIp.String(), destPort))
	if err == nil {
		defer upstreamConn.Close()
		defer conn.Close()
		tcppump.Pump2SpTcpTcp(conn.(*net.TCPConn), upstreamConn.(*net.TCPConn))
		return false, nil
	}

	return true, nil
}

func (t *tlsProxy) rewriteRequest(r *httputil.ProxyRequest) error {
	host := r.In.Host
	if host == "" {
		host = r.In.TLS.ServerName
	}

	r.SetURL(&url.URL{
		Scheme: "http",
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

	downstreamAddrStr, _, err := net.SplitHostPort(r.In.RemoteAddr)
	if err != nil {
		return err
	}
	downstreamIp := net.ParseIP(downstreamAddrStr)
	if downstreamIp == nil {
		return fmt.Errorf("could not parse as ip: %s", downstreamAddrStr)
	}

	newContext := context.WithValue(r.Out.Context(), mdnsContextKeyDownstreamIp, downstreamIp)
	r.Out = r.Out.WithContext(newContext)

	return nil
}

func (t *tlsProxy) dialUpstream(ctx context.Context, network, addr string) (net.Conn, error) {
	dialHost, dialPort, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	downstreamIp := ctx.Value(mdnsContextKeyDownstreamIp)
	is4 := true
	if downstreamIp != nil {
		is4 = downstreamIp.(net.IP).To4() != nil
	}

	proxyAddr, upstreamIp, err := t.getUpstream(dialHost, is4)
	if err != nil {
		return nil, err
	}

	// fall back to normal dialer
	dialer := &net.Dialer{}
	if downstreamIp != nil {
		dialer = dialerForTransparentBind(downstreamIp.(net.IP), t.getMark(proxyAddr))
	}

	return dialer.DialContext(ctx, "tcp", net.JoinHostPort(upstreamIp.String(), dialPort))
}
