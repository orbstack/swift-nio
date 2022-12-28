package tcpfwd

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmm/vnet/netutil"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type TcpHostForwarder struct {
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

	if addrPort.Addr().IsLoopback() && addrPort.Port() < 1024 {
		// Bypass privileged ports by listening on 0.0.0.0
		addr := net.IPv4zero
		if addrPort.Addr().Is6() {
			addr = net.IPv6zero
		}

		l, err := net.Listen("tcp", net.JoinHostPort(addr.String(), strconv.Itoa(int(addrPort.Port()))))
		return l, true, err
	}

	l, err := net.Listen("tcp", addr)
	return l, false, err
}

func StartTcpHostForward(s *stack.Stack, nicId tcpip.NICID, gatewayAddr4, gatewayAddr6, listenAddr, connectAddr4, connectAddr6 string, isInternal bool) error {
	listener, requireLoopback, err := ListenTCP(listenAddr)
	if err != nil {
		return err
	}

	connectAddrPort4, err := netip.ParseAddrPort(connectAddr4)
	if err != nil {
		return err
	}

	connectAddrPort6, err := netip.ParseAddrPort(connectAddr6)
	if err != nil {
		return err
	}

	f := &TcpHostForwarder{
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
	return nil
}

func (f *TcpHostForwarder) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *TcpHostForwarder) handleConn(conn net.Conn) {
	defer conn.Close()

	// Detect IPv4 or IPv6
	remoteAddr := conn.RemoteAddr().(*net.TCPAddr)
	proto := ipv4.ProtocolNumber
	connectAddr := f.connectAddr4
	if remoteAddr.IP.To4() == nil {
		proto = ipv6.ProtocolNumber
		connectAddr = f.connectAddr6
	}

	// Check remote address if using 0.0.0.0 to bypass privileged ports for loopback
	if f.requireLoopback && !remoteAddr.IP.IsLoopback() {
		fmt.Println("rejecting connection from non-loopback address", remoteAddr)
		return
	}

	// Spoof source address
	var srcAddr tcpip.Address
	if remoteAddr.IP.IsLoopback() {
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

	fmt.Println("dial from", virtSrcAddr, "to", connectAddr, "with proto", proto, "and timeout", tcpConnectTimeout)
	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(tcpConnectTimeout))
	defer cancel()
	virtConn, err := gonet.DialTCPWithBind(ctx, f.stack, virtSrcAddr, connectAddr, proto)
	if err != nil {
		return
	}
	defer virtConn.Close()

	pump2(conn, virtConn)
}

func (f *TcpHostForwarder) Stop() {
	f.listener.Close()
}
