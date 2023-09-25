package udpfwd

import (
	"errors"
	"net"
	"os"
	"syscall"

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

func dialUDPSourceBind(srcPort int, daddr *net.UDPAddr) (*net.UDPConn, error) {
	ip4 := daddr.IP.To4()
	family := syscall.AF_INET
	if ip4 == nil {
		family = syscall.AF_INET6
	}

	// do it ourselves to set socket options
	syscall.ForkLock.RLock()
	sfd, err := unix.Socket(family, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		syscall.ForkLock.RUnlock()
		return nil, err
	}

	// set O_CLOEXEC and O_NONBLOCK
	unix.CloseOnExec(sfd)
	syscall.ForkLock.RUnlock()
	unix.SetNonblock(sfd, true)

	// need to set SO_REUSEPORT to fix tailscale MappingVariesByDestIP causing DERP to be used
	// it allows reusing src-dest 5-tuple
	// unlike Linux it does NOT cause load balancing. same 5-tuple will return EADDRINUSE
	if err := unix.SetsockoptInt(sfd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
		unix.Close(sfd)
		return nil, err
	}

	// bind to source port
	if ip4 != nil {
		err = unix.Bind(sfd, &unix.SockaddrInet4{
			Port: srcPort,
		})
	} else {
		err = unix.Bind(sfd, &unix.SockaddrInet6{
			Port: srcPort,
		})
	}
	if err != nil {
		unix.Close(sfd)
		return nil, err
	}

	// connect to dest
	if ip4 != nil {
		err = unix.Connect(sfd, &unix.SockaddrInet4{
			Port: daddr.Port,
			Addr: [4]byte(ip4),
		})
	} else {
		err = unix.Connect(sfd, &unix.SockaddrInet6{
			Port: daddr.Port,
			Addr: [16]byte(daddr.IP),
		})
	}
	if err != nil {
		unix.Close(sfd)
		return nil, err
	}

	file := os.NewFile(uintptr(sfd), "udp conn")
	conn, err := net.FileConn(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	file.Close()

	return conn.(*net.UDPConn), nil
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
			conn, err := dialUDPSourceBind(int(r.ID().LocalPort), dialDestAddr)
			if err == nil {
				return conn, nil
			}
			// also bail out if it's not an address-in-use error
			if err != nil && !errors.Is(err, unix.EADDRINUSE) {
				return nil, err
			}

			// explicit bind is conservative. fall back to dynamic if port is used
			// too much mutex contention when running amass
			/*
				logrus.WithFields(logrus.Fields{
					"localPort": r.ID().LocalPort,
					"remote":    dialDestAddr,
				}).WithError(err).Debug("explicit UDP dial failed")
			*/

			return net.DialUDP("udp", nil, dialDestAddr)
		}, true)
		if err != nil {
			logrus.Error("NewUDPProxy() =", err)
			return
		}

		go proxy.Run(true)
	})
}
