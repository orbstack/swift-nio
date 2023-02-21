package udpfwd

import (
	"net"
	"strconv"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

type icmpSender interface {
	InjectDestUnreachable6(stack.PacketBufferPtr, header.ICMPv6Code) error
}

func NewUdpForwarder(s *stack.Stack, natTable map[tcpip.Address]tcpip.Address, natLock *sync.Mutex, i icmpSender) *udp.Forwarder {
	// can't move to goroutine - packet ref issue: PullUp failed; see udp-goroutine-panic.log
	// happens with DNS packets (to 192.168.66.1 nameserver)
	return udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		localAddress := r.ID().LocalAddress
		if !netutil.ShouldProxy(localAddress) {
			return
		}

		natLock.Lock()
		if replaced, ok := natTable[localAddress]; ok {
			localAddress = replaced
		}
		natLock.Unlock()

		var wq waiter.Queue
		ep, tcpErr := r.CreateEndpoint(&wq)
		if tcpErr != nil {
			logrus.Error("r.CreateEndpoint() =", tcpErr)
			return
		}

		// TTL info
		ep.SocketOptions().SetReceiveTTL(true)
		ep.SocketOptions().SetReceiveHopLimit(true)

		extAddr := net.JoinHostPort(localAddress.String(), strconv.Itoa(int(r.ID().LocalPort)))
		proxy, err := NewUDPProxy(&autoStoppingListener{UDPConn: gonet.NewUDPConn(s, &wq, ep)}, func(fromAddr *net.UDPAddr) (net.Conn, error) {
			return net.Dial("udp", extAddr)
		}, true)
		if err != nil {
			logrus.Error("NewUDPProxy() =", err)
			return
		}

		go proxy.Run(true)
	})
}
