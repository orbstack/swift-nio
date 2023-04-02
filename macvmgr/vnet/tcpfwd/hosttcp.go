package tcpfwd

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type TcpHostForward struct {
	listener        net.Listener
	requireLoopback bool
	connectAddr4    tcpip.FullAddress
	connectAddr6    tcpip.FullAddress
	gatewayAddr4    tcpip.Address
	gatewayAddr6    tcpip.Address
	stack           *stack.Stack
	nicId           tcpip.NICID
	// whether this port forward is an internal implementation detail
	// if so, spoof gateway ip for localhost, not external ip
	isInternal bool
}

func ListenTCP(addr string) (net.Listener, bool, error) {
	addrPort, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, false, err
	}

	// disable tcp46 for IPv4-only. we only do 4-6 for v6 listeners
	network := "tcp4"
	if addrPort.Addr().Is6() {
		network = "tcp"
	}

	if addrPort.Addr().IsLoopback() && addrPort.Port() < 1024 {
		// Bypass privileged ports by listening on 0.0.0.0
		addr := net.IPv4zero
		if addrPort.Addr().Is6() {
			addr = net.IPv6zero
			// disable 4-in-6. if we intended to bind to localhost, then we only want v6.
			// there's no 4-in-6 for non-0000 addresses.
			network = "tcp6"
		}

		l, err := net.Listen(network, net.JoinHostPort(addr.String(), strconv.Itoa(int(addrPort.Port()))))
		return l, true, err
	}

	l, err := net.Listen(network, addr)
	return l, false, err
}

func StartTcpHostForward(s *stack.Stack, nicId tcpip.NICID, gatewayAddr4, gatewayAddr6, listenAddr, connectAddr4, connectAddr6 string, isInternal bool) (*TcpHostForward, error) {
	listener, requireLoopback, err := ListenTCP(listenAddr)
	if err != nil {
		return nil, err
	}

	connectAddrPort4, err := netip.ParseAddrPort(connectAddr4)
	if err != nil {
		return nil, err
	}

	connectAddrPort6, err := netip.ParseAddrPort(connectAddr6)
	if err != nil {
		return nil, err
	}

	f := &TcpHostForward{
		listener:        listener,
		requireLoopback: requireLoopback,
		connectAddr4: tcpip.FullAddress{
			NIC:  nicId,
			Addr: tcpip.Address(connectAddrPort4.Addr().AsSlice()),
			Port: uint16(connectAddrPort4.Port()),
		},
		connectAddr6: tcpip.FullAddress{
			NIC:  nicId,
			Addr: tcpip.Address(connectAddrPort6.Addr().AsSlice()),
			Port: uint16(connectAddrPort6.Port()),
		},
		gatewayAddr4: netutil.ParseTcpipAddress(gatewayAddr4),
		gatewayAddr6: netutil.ParseTcpipAddress(gatewayAddr6),
		stack:        s,
		nicId:        nicId,
		isInternal:   isInternal,
	}

	go f.listen()
	return f, nil
}

func (f *TcpHostForward) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *TcpHostForward) handleConn(conn net.Conn) {
	defer conn.Close()

	// Detect IPv4 or IPv6
	remoteAddr := conn.RemoteAddr().(*net.TCPAddr)
	proto := ipv4.ProtocolNumber
	connectAddr := f.connectAddr4
	// 4-in-6 means the listener is v6, so the other side must be v6
	is4in6 := remoteAddr.AddrPort().Addr().Is4In6()
	if is4in6 || remoteAddr.IP.To4() == nil {
		proto = ipv6.ProtocolNumber
		connectAddr = f.connectAddr6
	}

	// Check remote address if using 0.0.0.0 to bypass privileged ports for loopback
	if f.requireLoopback && !remoteAddr.IP.IsLoopback() {
		logrus.Debug("rejecting connection from non-loopback address", remoteAddr)
		return
	}

	// Spoof source address
	var srcAddr tcpip.Address
	//TODO fix source addr
	if true {
		// We can't spoof loopback. Look up the host's default address.
		if proto == ipv4.ProtocolNumber {
			srcAddr = tcpip.Address(netutil.GetDefaultAddress4())
			// Fallback = gateway (i.e. if airplane mode)
			if srcAddr == "" || f.isInternal {
				srcAddr = f.gatewayAddr4
			}
		} else {
			srcAddr = tcpip.Address(netutil.GetDefaultAddress6())
			// Fallback = gateway (i.e. if airplane mode)
			if srcAddr == "" || f.isInternal {
				srcAddr = f.gatewayAddr6
			}
		}
	} else {
		srcAddr = tcpip.Address(remoteAddr.IP)
	}

	virtSrcAddr := tcpip.FullAddress{
		NIC:  f.nicId,
		Addr: srcAddr,
		Port: uint16(remoteAddr.Port),
	}

	logrus.WithFields(logrus.Fields{
		"src":     virtSrcAddr,
		"dst":     connectAddr,
		"proto":   proto,
		"timeout": tcpConnectTimeout,
	}).Trace("dial")
	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(tcpConnectTimeout))
	defer cancel()
	virtConn, err := gonet.DialTCPWithBind(ctx, f.stack, virtSrcAddr, connectAddr, proto)
	if err != nil {
		logrus.WithError(err).WithField("addr", connectAddr).Error("host-tcp forward: dial failed")
		return
	}
	defer virtConn.Close()

	// other port doesn't matter, only service does (client port should be ephemeral)
	err = setExtNodelay(conn.(*net.TCPConn), 0)
	if err != nil {
		logrus.Errorf("set ext opts failed ", err)
		return
	}

	pump2(conn.(*net.TCPConn), virtConn)
}

func (f *TcpHostForward) Close() error {
	return f.listener.Close()
}
