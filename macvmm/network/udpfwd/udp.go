package udpfwd

import (
	"net"
	"strconv"
	"sync"

	"github.com/kdrag0n/macvirt/macvmm/network/gonet"
	log "github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

func NewUdpForwarder(s *stack.Stack, nat map[tcpip.Address]tcpip.Address, natLock *sync.Mutex) *udp.Forwarder {
	return udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		localAddress := r.ID().LocalAddress

		natLock.Lock()
		if replaced, ok := nat[localAddress]; ok {
			localAddress = replaced
		}
		natLock.Unlock()

		var wq waiter.Queue
		ep, tcpErr := r.CreateEndpoint(&wq)
		if tcpErr != nil {
			log.Errorf("r.CreateEndpoint() = %v", tcpErr)
			return
		}

		// TTL info
		ep.SocketOptions().SetReceiveHopLimit(true)
		ep.SocketOptions().SetReceiveTTL(true)

		extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(int(r.ID().LocalPort)))
		p, _ := NewUDPProxy(&autoStoppingListener{underlying: gonet.NewUDPConn(s, &wq, ep)}, func() (net.Conn, error) {
			return net.Dial("udp", extAddr)
		})
		go p.Run()
	})
}
