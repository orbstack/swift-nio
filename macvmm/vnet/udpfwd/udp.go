package udpfwd

import (
	"net"
	"strconv"
	"sync"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmm/vnet/netutil"
	log "github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

func NewUdpForwarder(s *stack.Stack, natTable map[tcpip.Address]tcpip.Address, natLock *sync.RWMutex) *udp.Forwarder {
	return udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		go func() {
			localAddress := r.ID().LocalAddress
			if !netutil.ShouldProxy(localAddress) {
				return
			}

			natLock.RLock()
			if replaced, ok := natTable[localAddress]; ok {
				localAddress = replaced
			}
			natLock.RUnlock()

			var wq waiter.Queue
			ep, tcpErr := r.CreateEndpoint(&wq)
			if tcpErr != nil {
				log.Errorf("r.CreateEndpoint() = %v", tcpErr)
				return
			}

			// TTL info
			ep.SocketOptions().SetReceiveTTL(true)
			ep.SocketOptions().SetReceiveHopLimit(true)

			extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(int(r.ID().LocalPort)))
			p, _ := NewUDPProxy(&autoStoppingListener{underlying: gonet.NewUDPConn(s, &wq, ep)}, func() (net.Conn, error) {
				return net.Dial("udp", extAddr)
			})
			go p.Run(true)
		}()
	})
}
