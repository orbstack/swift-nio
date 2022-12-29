package tcpfwd

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
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

func NewTcpForwarder(s *stack.Stack, natTable map[tcpip.Address]tcpip.Address, natLock *sync.RWMutex) *tcp.Forwarder {
	return tcp.NewForwarder(s, 0, listenBacklog, func(r *tcp.ForwarderRequest) {
		// Workaround for NFS panic
		defer func() {
			if err := recover(); err != nil {
				logrus.Error("tcpfwd: panic in forwarder", err)
			}
		}()

		localAddress := r.ID().LocalAddress
		if !netutil.ShouldProxy(localAddress) {
			r.Complete(false)
			return
		}

		natLock.RLock()
		if replaced, ok := natTable[localAddress]; ok {
			localAddress = replaced
		}
		natLock.RUnlock()
		extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(int(r.ID().LocalPort)))

		extConn, err := net.DialTimeout("tcp", extAddr, tcpConnectTimeout)
		if err != nil {
			logrus.Errorf("TCP forward [%v] dial failed: %v", extAddr, err)
			// if connection refused
			if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, unix.ECONNRESET) {
				// send RST
				r.Complete(true)
			} else if errors.Is(err, unix.EHOSTUNREACH) || errors.Is(err, unix.EHOSTDOWN) || errors.Is(err, unix.ENETUNREACH) {
				// TODO: icmp response
				r.Complete(false)
			} else if errors.Is(err, unix.ETIMEDOUT) {
				r.Complete(false)
			} else {
				// unknown
				r.Complete(false)
			}
			return
		}
		defer extConn.Close()

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

		pump2(virtConn, extConn.(*net.TCPConn))
	})
}
