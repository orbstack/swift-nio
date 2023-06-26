package udpfwd

import (
	"errors"
	"net"

	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/gvaddr"
	"github.com/orbstack/macvirt/vmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
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

		// remember: local = target (because we're acting as proxy)
		dialDestAddr := &net.UDPAddr{
			IP:   net.IP(localAddress.AsSlice()),
			Port: int(r.ID().LocalPort),
		}
		proxy, err := NewUDPProxy(&autoStoppingListener{UDPConn: gonet.NewUDPConn(s, &wq, ep)}, func(fromAddr *net.UDPAddr) (net.Conn, error) {
			// try to reuse the source port if possible
			// this helps preserve connection after conntrack timeouts, as it's expected that Docker host net doesn't involve NAT and thus will never time out
			// do it conservatively, without SO_REUSEADDR or SO_REUSEPORT, to avoid port conflicts
			// not needed for external (non-loopback) conns because there's usually internet NAT anyway
			// remote port = VM client port
			conn, err := net.DialUDP("udp", &net.UDPAddr{
				Port: int(r.ID().RemotePort),
			}, dialDestAddr)
			if err == nil {
				return conn, nil
			}
			// also bail out if it's not an address-in-use error
			if err != nil && !errors.Is(err, unix.EADDRINUSE) {
				return nil, err
			}

			// explicit bind is conservative. fall back to dynamic if port is used
			logrus.WithFields(logrus.Fields{
				"localPort": r.ID().LocalPort,
				"remote":    dialDestAddr,
			}).WithError(err).Debug("explicit UDP dial failed")

			return net.DialUDP("udp", nil, dialDestAddr)
		}, true)
		if err != nil {
			logrus.Error("NewUDPProxy() =", err)
			return
		}

		go proxy.Run(true)
	})
}
