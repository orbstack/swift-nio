package tcpfwd

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmm/vnet/netutil"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	connectTimeout = 30 * time.Second
)

type TcpHostForwarder struct {
	listener     net.Listener
	connectAddr4 tcpip.FullAddress
	connectAddr6 tcpip.FullAddress
	gatewayAddr4 tcpip.Address
	gatewayAddr6 tcpip.Address
	stack        *stack.Stack
	nicId        tcpip.NICID
}

func StartTcpHostForward(s *stack.Stack, nicId tcpip.NICID, gatewayAddr4, gatewayAddr6, listenAddr, connectAddr4, connectAddr6 string) error {
	listener, err := net.Listen("tcp", listenAddr)
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
		listener: listener,
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

	// Spoof source address
	var srcAddr tcpip.Address
	if remoteAddr.IP.IsLoopback() {
		// We can't spoof loopback. Look up the host's default address.
		if proto == ipv4.ProtocolNumber {
			srcAddr = tcpip.Address(netutil.GetDefaultAddress4())
			// Fallback = gateway (i.e. if airplane mode)
			if srcAddr == "" {
				srcAddr = f.gatewayAddr4
			}
		} else {
			srcAddr = tcpip.Address(netutil.GetDefaultAddress6())
			// Fallback = gateway (i.e. if airplane mode)
			if srcAddr == "" {
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

	fmt.Println("dial from", virtSrcAddr, "to", connectAddr, "with proto", proto, "and timeout", connectTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(connectTimeout))
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
