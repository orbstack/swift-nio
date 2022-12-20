package network

import (
	"fmt"
	"io"
	"net"
	"sync"

	log "github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/waiter"
)

func pump(errc chan<- error, src, dst net.Conn) {
	buf := make([]byte, 512*1024)
	_, err := io.CopyBuffer(src, dst, buf)
	errc <- err
}

func newTcpForwarder(s *stack.Stack, nat map[tcpip.Address]tcpip.Address, natLock *sync.Mutex) *tcp.Forwarder {
	return tcp.NewForwarder(s, 0, 10, func(r *tcp.ForwarderRequest) {
		localAddress := r.ID().LocalAddress

		natLock.Lock()
		if replaced, ok := nat[localAddress]; ok {
			localAddress = replaced
		}
		natLock.Unlock()
		var externalAddr string
		if localAddress.To4() != "" {
			externalAddr = fmt.Sprintf("%s:%d", localAddress, r.ID().LocalPort)
		} else {
			externalAddr = fmt.Sprintf("[%s]:%d", localAddress, r.ID().LocalPort)
		}
		outbound, err := net.Dial("tcp", externalAddr)
		if err != nil {
			log.Tracef("net.Dial() = %v", err)
			r.Complete(true)
			return
		}
		defer outbound.Close()

		var wq waiter.Queue
		ep, tcpErr := r.CreateEndpoint(&wq)
		r.Complete(false)
		if tcpErr != nil {
			log.Errorf("r.CreateEndpoint() = %v", tcpErr)
			return
		}

		inbound := gonet.NewTCPConn(&wq, ep)
		defer inbound.Close()

		errc := make(chan error, 1)
		go pump(errc, inbound, outbound)
		go pump(errc, outbound, inbound)
		<-errc
	})
}
