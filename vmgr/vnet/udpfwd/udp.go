package udpfwd

import (
	"fmt"
	"net"
	"os"
	"sync"
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
)

const verboseDebug = false

type udpManager struct {
	// write once, read many times
	// TODO separate v4 and v6
	srcPortMap sync.Map // map[int]int

	s          *stack.Stack
	i          icmpSender
	hostNatIP4 tcpip.Address
	hostNatIP6 tcpip.Address
}

type icmpSender interface {
	InjectDestUnreachable6(stack.PacketBufferPtr, header.ICMPv6Code) error
}

// private API
type udpPrivateEndpoint interface {
	HandlePacket(id stack.TransportEndpointID, pkt stack.PacketBufferPtr)
}

// SO_REUSEPORT requires an explicit IP bind, not wildcard
// this is slow but no choice
// caching this is racy and error prone in case of diff VPN routes
// so we have to take the hit for applications like amass
// TODO; consider custom demux for up to 64k local sockets?
// helps with amass perf
func getLaddrForDest(dest *net.UDPAddr) (net.IP, error) {
	conn, err := net.DialUDP("udp", nil, dest)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP, nil
}

func (m *udpManager) dialUDPSourceBind(srcPort int, daddr *net.UDPAddr, srcWildcard bool) (*net.UDPConn, error) {
	destIP4 := daddr.IP.To4()
	family := unix.AF_INET
	if destIP4 == nil {
		family = unix.AF_INET6
	}

	// do it ourselves to set socket options
	syscall.ForkLock.RLock()
	sfd, err := unix.Socket(family, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err == nil {
		unix.CloseOnExec(sfd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	// set O_NONBLOCK and set up file
	unix.SetNonblock(sfd, true)
	file := os.NewFile(uintptr(sfd), "udp conn")
	// always closed:
	// on error, close early
	// on success, handed off to net.FileConn which does a dup
	defer file.Close()

	// need to set SO_REUSEPORT to fix tailscale MappingVariesByDestIP causing DERP to be used
	// it allows reusing src-dest 5-tuple
	// unlike Linux it does NOT cause load balancing. same 5-tuple will return EADDRINUSE
	if err := unix.SetsockoptInt(sfd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		return nil, fmt.Errorf("setsockopt: %w", err)
	}
	if err := unix.SetsockoptInt(sfd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
		return nil, fmt.Errorf("setsockopt: %w", err)
	}

	var srcIP net.IP
	if srcWildcard {
		if family == unix.AF_INET {
			srcIP = net.IPv4zero
		} else {
			srcIP = net.IPv6zero
		}
	} else {
		srcIP, err = getLaddrForDest(daddr)
		if err != nil {
			return nil, fmt.Errorf("resolve route: %w", err)
		}
	}

	// bind to source port, plus explicit IP
	if family == unix.AF_INET {
		err = unix.Bind(sfd, &unix.SockaddrInet4{
			Addr: [4]byte(srcIP.To4()),
			Port: srcPort,
		})
	} else {
		err = unix.Bind(sfd, &unix.SockaddrInet6{
			Addr: [16]byte(srcIP.To16()),
			Port: srcPort,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("bind: %w", err)
	}

	// connect to dest
	if family == unix.AF_INET {
		err = unix.Connect(sfd, &unix.SockaddrInet4{
			Port: daddr.Port,
			Addr: [4]byte(destIP4),
		})
	} else {
		err = unix.Connect(sfd, &unix.SockaddrInet6{
			Port: daddr.Port,
			Addr: [16]byte(daddr.IP),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	conn, err := net.FileConn(file)
	if err != nil {
		return nil, fmt.Errorf("fileconn: %w", err)
	}

	return conn.(*net.UDPConn), nil
}

func (m *udpManager) handleNewPacket(r *udp.ForwarderRequest) {
	localAddress := r.ID().LocalAddress
	if !netutil.ShouldForward(localAddress) {
		return
	}

	// host NAT: match source v4/v6
	// can't fall back for UDP because we don't know if anyone received it
	if localAddress == m.hostNatIP4 {
		localAddress = gvaddr.LoopbackGvIP4
	} else if localAddress == m.hostNatIP6 {
		localAddress = gvaddr.LoopbackGvIP6
	}

	// like r.CreateEndpoint, but unconnected raddr
	// the server so this allows reuse
	// should also help with amass: less endpoints to iterate through
	epConn, err := gonet.DialUDP(m.s, &tcpip.FullAddress{
		NIC:  r.Packet().NICID,
		Addr: r.ID().LocalAddress,
		Port: r.ID().LocalPort,
	}, nil, r.Packet().NetworkProtocolNumber)
	if err != nil {
		logrus.WithError(err).Error("create UDP endpoint failed")
		return
	}
	ep := epConn.Endpoint()

	// TTL info
	ep.SocketOptions().SetReceiveTTL(true)
	ep.SocketOptions().SetReceiveHopLimit(true)

	// inject this packet like r.CreateEndpoint
	// TODO: could drop packets in bind race? but r.CreateEndpoint is no diff...
	ep.(udpPrivateEndpoint).HandlePacket(r.ID(), r.Packet())

	if verboseDebug {
		logrus.WithFields(logrus.Fields{
			"src":   r.ID().LocalAddress,
			"srcP":  r.ID().LocalPort,
			"dest":  r.ID().RemoteAddress,
			"destP": r.ID().RemotePort,
		}).Debug("UDP forwarder: new endpoint")
	}

	// remember: local = target (because we're acting as proxy)
	dialDestAddr := &net.UDPAddr{
		IP:   net.IP(localAddress.AsSlice()),
		Port: int(r.ID().LocalPort),
	}
	proxy, err := NewUDPProxy(&autoStoppingListener{UDPConn: epConn}, func(fromAddr *net.UDPAddr) (net.Conn, error) {
		reqSrcPort := fromAddr.Port

		// map the source port, if there's a mapping for this port
		// keeps mtr *and* Tailscale happy: it uses privileged <1024 ports
		mappedSrcPort := reqSrcPort
		if v, ok := m.srcPortMap.Load(reqSrcPort); ok {
			mappedSrcPort = v.(int)
		}

		// try to reuse the source port if possible
		// this helps preserve connection after conntrack timeouts, as it's expected that Docker host net doesn't involve NAT and thus will never time out
		// do it conservatively, without SO_REUSEADDR or SO_REUSEPORT, to avoid port conflicts
		// not needed for external (non-loopback) conns because there's usually internet NAT anyway
		// remote port = VM client port
		conn, err := m.dialUDPSourceBind(mappedSrcPort, dialDestAddr, false)
		if err == nil {
			return conn, nil
		}
		// could get:
		// - EADDRINUSE: if used by another process
		// - EACCES: privileged port
		//   * could fix this by giving up SO_REUSEPORT and retrying with wildcard, but not worth it. NATs are allowed to translate src port and we are officially NAT
		// - No route to host: race w/ route change
		// so always fall back to dynamic bind

		// explicit bind is conservative. fall back to dynamic if port is used
		// too much mutex contention when running amass
		logrus.WithFields(logrus.Fields{
			"localPort": reqSrcPort,
			"remote":    dialDestAddr,
		}).WithError(err).Debug("explicit UDP dial failed")

		conn, err = net.DialUDP("udp", nil, dialDestAddr)
		if err != nil {
			return nil, err
		}

		// explicit dial failed, so we got a new mapping for this src port. remember it
		m.srcPortMap.Store(reqSrcPort, conn.LocalAddr().(*net.UDPAddr).Port)

		return conn, nil
	}, true)
	if err != nil {
		logrus.Error("NewUDPProxy() =", err)
		return
	}

	go proxy.Run(true)
}

func NewUdpForwarder(s *stack.Stack, i icmpSender, hostNatIP4 tcpip.Address, hostNatIP6 tcpip.Address) *udp.Forwarder {
	m := &udpManager{
		s:          s,
		i:          i,
		hostNatIP4: hostNatIP4,
		hostNatIP6: hostNatIP6,
	}
	// can't move to goroutine - packet ref issue: PullUp failed; see udp-goroutine-panic.log
	// happens with DNS packets (to 192.168.66.1 nameserver)
	return udp.NewForwarder(s, m.handleNewPacket)
}
