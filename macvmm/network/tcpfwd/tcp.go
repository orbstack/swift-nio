package tcpfwd

import (
	"errors"
	"io"
	"net"
	"strconv"
	"sync"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

func pump1(errc chan<- error, src, dst net.Conn) {
	buf := make([]byte, 512*1024)
	_, err := io.CopyBuffer(src, dst, buf)

	// half-close to allow graceful shutdown
	if dstTcp, ok := dst.(*net.TCPConn); ok {
		dstTcp.CloseWrite()
	}
	if dstTcp, ok := src.(*gonet.TCPConn); ok {
		dstTcp.CloseWrite()
	}

	if srcTcp, ok := src.(*net.TCPConn); ok {
		srcTcp.CloseRead()
	}
	if srcTcp, ok := dst.(*gonet.TCPConn); ok {
		srcTcp.CloseRead()
	}

	errc <- err
}

func pump2(c1, c2 net.Conn) {
	errChan := make(chan error, 2)
	go pump1(errChan, c1, c2)
	go pump1(errChan, c2, c1)

	// Don't wait for both if one side failed (not EOF)
	if err1 := <-errChan; err1 != nil {
		return
	}
	if err2 := <-errChan; err2 != nil {
		return
	}
}

func NewTcpForwarder(s *stack.Stack, nat map[tcpip.Address]tcpip.Address, natLock *sync.Mutex) *tcp.Forwarder {
	return tcp.NewForwarder(s, 0, 10, func(r *tcp.ForwarderRequest) {
		localAddress := r.ID().LocalAddress

		natLock.Lock()
		if replaced, ok := nat[localAddress]; ok {
			localAddress = replaced
		}
		natLock.Unlock()
		extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(int(r.ID().LocalPort)))

		extConn, err := net.Dial("tcp", extAddr)
		if err != nil {
			log.Tracef("net.Dial() = %v", err)
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
			log.Errorf("r.CreateEndpoint() = %v", tcpErr)
			return
		}

		virtConn := gonet.NewTCPConn(&wq, ep)
		defer virtConn.Close()

		pump2(virtConn, extConn)
	})
}
