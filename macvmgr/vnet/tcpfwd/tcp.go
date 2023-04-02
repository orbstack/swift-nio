package tcpfwd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/icmpfwd"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
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

func NewTcpForwarder(s *stack.Stack, i *icmpfwd.IcmpFwd, hostNatIP4 tcpip.Address, hostNatIP6 tcpip.Address) (*tcp.Forwarder, *ProxyManager) {
	proxyMgr := newProxyManager(hostNatIP4, hostNatIP6)

	return tcp.NewForwarder(s, 0, listenBacklog, func(r *tcp.ForwarderRequest) {
		refDec := false
		defer func() {
			if !refDec {
				r.Pkt.DecRef()
			}
		}()

		localAddress := r.ID().LocalAddress
		if !netutil.ShouldProxy(localAddress) {
			r.Complete(false)
			return
		}

		// this also handles host NAT
		extConn, extAddr, err := proxyMgr.DialForward(localAddress, int(r.ID().LocalPort))
		if err != nil {
			logrus.Debugf("TCP forward [%v] dial failed: %v", extAddr, err)
			// if connection refused
			if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, unix.ECONNRESET) {
				// send RST
				r.Complete(true)
			} else if errors.Is(err, unix.EHOSTUNREACH) || errors.Is(err, unix.EHOSTDOWN) || errors.Is(err, unix.ENETUNREACH) {
				logrus.Debug("inject ICMP unreachable")
				if localAddress.To4() == "" {
					if errors.Is(err, unix.ENETUNREACH) {
						i.InjectDestUnreachable6(r.Pkt, header.ICMPv6NetworkUnreachable)
					} else {
						i.InjectDestUnreachable6(r.Pkt, header.ICMPv6AddressUnreachable)
					}
				} else {
					if errors.Is(err, unix.ENETUNREACH) {
						i.InjectDestUnreachable4(r.Pkt, header.ICMPv4NetUnreachable)
					} else {
						i.InjectDestUnreachable4(r.Pkt, header.ICMPv4HostUnreachable)
					}
				}
				r.Complete(false)
			} else if errors.Is(err, unix.ETIMEDOUT) || errors.Is(err, context.DeadlineExceeded) {
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
			// Maybe VM abandoned the connection already, nothing to do
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
			err = setExtNodelay(extTcpConn, virtConn.RemoteAddr().(*net.TCPAddr).Port)
			if err != nil {
				logrus.Errorf("TCP forward [%v] set ext opts failed: %v", extAddr, err)
				return
			}
		}

		pump2(virtConn, extConn)
	}), proxyMgr
}
