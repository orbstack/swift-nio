package udpfwd

import (
	"net"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gvaddr"
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

func NewUdpForwarder(s *stack.Stack, i icmpSender, hostNatIP4 tcpip.Address, hostNatIP6 tcpip.Address) *udp.Forwarder {
	// can't move to goroutine - packet ref issue: PullUp failed; see udp-goroutine-panic.log
	// happens with DNS packets (to 192.168.66.1 nameserver)
	return udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		localAddress := r.ID().LocalAddress
		if !netutil.ShouldForward(localAddress) {
			return
		}

		// host NAT: match source v4/v6
		// can't fall back for UDP because we don't know if anyone received it
		if localAddress == hostNatIP4 {
			localAddress = gvaddr.LoopbackGvIP4
		} else if localAddress == hostNatIP6 {
			localAddress = gvaddr.LoopbackGvIP6
		}

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
