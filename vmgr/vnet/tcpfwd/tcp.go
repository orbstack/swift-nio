package tcpfwd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/orbstack/macvirt/vmgr/vnet/bridge"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/icmpfwd"
	"github.com/orbstack/macvirt/vmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	// note: Linux default is 60 sec
	tcpConnectTimeout = 30 * time.Second
	// this is global
	// set very high for nmap
	listenBacklog = 65535
)

func tryClose(conn *gonet.TCPConn) (err error) {
	defer func() {
		if err := recover(); err != nil {
			err = fmt.Errorf("tcpfwd: close panic: %v", err)
		}
	}()

	conn.Close()
	return
}

func tryAbort(conn *gonet.TCPConn) (err error) {
	defer func() {
		if err := recover(); err != nil {
			err = fmt.Errorf("tcpfwd: abort panic: %v", err)
		}
	}()

	conn.Endpoint().Abort()
	return
}

func tryBestCleanup(conn *gonet.TCPConn) error {
	err := tryClose(conn)
	if err != nil {
		return err
	}
	return tryAbort(conn)
}

func NewTcpForwarder(s *stack.Stack, icmpMgr *icmpfwd.IcmpFwd, hostNatIP4 tcpip.Address, hostNatIP6 tcpip.Address, bridgeRouteMon *bridge.RouteMon) (*tcp.Forwarder, *ProxyManager) {
	proxyMgr := newProxyManager(hostNatIP4, hostNatIP6)

	return tcp.NewForwarder(s, 0, listenBacklog, func(r *tcp.ForwarderRequest) {
		refDec := false
		defer func() {
			if !refDec {
				r.Pkt.DecRef()
			}
		}()

		targetAddr := r.ID().LocalAddress
		// exclude blacklisted IPs
		if !netutil.ShouldForward(targetAddr) {
			r.Complete(false)
			return
		}
		// and to prevent loops, exclude IPs that we're currently bridging (i.e. scon or vlan)
		// TODO: restore this check? loops are inconsequential, but incorrectly excluding a conflicted bridge route is not. conflicted routes will still be in route mon
		/*
			if bridgeRouteMon.ContainsIP(netutil.NetipFromAddr(targetAddr)) {
				logrus.WithField("ip", targetAddr).Debug("TCP forward: dropping looped conn")
				r.Complete(false)
				return
			}
		*/

		// if we require proxy and don't have SOCKS, port 80 should use reverse proxy
		extPort := int(r.ID().LocalPort)
		if proxyMgr.requiresHttpProxy && extPort == 80 && proxyMgr.isProxyEligibleIPPre(targetAddr) {
			proxyMgr.httpMu.Lock()
			revProxy := proxyMgr.httpRevProxy
			proxyMgr.httpMu.Unlock()
			if revProxy == nil {
				logrus.Error("TCP forward: missing HTTP reverse proxy")
				r.Complete(false)
				return
			}

			r.Pkt.DecRef()
			refDec = true

			var wq waiter.Queue
			ep, tcpErr := r.CreateEndpoint(&wq)
			r.Complete(false)
			if tcpErr != nil {
				// Maybe VM abandoned the connection already, nothing to do
				extAddr := net.JoinHostPort(targetAddr.String(), strconv.Itoa(extPort))
				logrus.Errorf("TCP forward [%v] create endpoint failed: %v", extAddr, tcpErr)
				return
			}

			virtConn := gonet.NewTCPConn(&wq, ep)
			// we don't defer close - http server will do it

			// TODO: dial first and handle conn refused, etc. correctly
			revProxy.HandleConn(virtConn)
			return
		}

		// this also handles host NAT
		extConn, extAddr, err := proxyMgr.DialForward(targetAddr, extPort)
		if err != nil {
			// log level depends on proxy
			if _, ok := err.(*ProxyDialError); ok {
				logrus.Warnf("TCP forward [%v] dial failed: %v", extAddr, err)
			} else {
				logrus.Debugf("TCP forward [%v] dial failed: %v", extAddr, err)
			}

			if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, unix.ECONNRESET) {
				// connection refused: send RST
				r.Complete(true)
			} else if errors.Is(err, unix.EHOSTUNREACH) || errors.Is(err, unix.EHOSTDOWN) || errors.Is(err, unix.ENETUNREACH) {
				logrus.Debug("inject ICMP unreachable")
				if targetAddr.To4() == (tcpip.Address{}) {
					if errors.Is(err, unix.ENETUNREACH) {
						icmpMgr.InjectDestUnreachable6(r.Pkt, header.ICMPv6NetworkUnreachable)
					} else {
						icmpMgr.InjectDestUnreachable6(r.Pkt, header.ICMPv6AddressUnreachable)
					}
				} else {
					if errors.Is(err, unix.ENETUNREACH) {
						icmpMgr.InjectDestUnreachable4(r.Pkt, header.ICMPv4NetUnreachable)
					} else {
						icmpMgr.InjectDestUnreachable4(r.Pkt, header.ICMPv4HostUnreachable)
					}
				}
				r.Complete(false)
			} else if errors.Is(err, unix.ETIMEDOUT) || errors.Is(err, context.DeadlineExceeded) {
				// timeout: simulate timeout by not responding
				r.Complete(false)
			} else {
				// unknown
				r.Complete(false)
			}
			return
		}
		defer extConn.Close()
		r.Pkt.DecRef()
		refDec = true

		var wq waiter.Queue
		ep, tcpErr := r.CreateEndpoint(&wq)
		r.Complete(false)
		if tcpErr != nil {
			// VM abandoned the connection already, nothing to do
			logrus.Errorf("TCP forward [%v] create endpoint failed: %v", extAddr, tcpErr)
			return
		}

		virtConn := gonet.NewTCPConn(&wq, ep)
		defer func() {
			err := tryBestCleanup(virtConn)
			if err != nil {
				logrus.Error("tcpfwd: cleanup panic", err)
			}
		}()

		if extTcpConn, ok := extConn.(*net.TCPConn); ok {
			// other port doesn't matter, only service does (client port should be ephemeral)
			err = setExtNodelay(extTcpConn, 0)
			if err != nil {
				logrus.Errorf("TCP forward [%v] set opts failed: %v", extAddr, err)
				return
			}

			// fast path, specialized for non-proxy TCP
			pump2SpTcpGv(extTcpConn, virtConn)
		} else {
			// generic (proxy case / TLS)
			pump2(extConn, virtConn)
		}
	}), proxyMgr
}
