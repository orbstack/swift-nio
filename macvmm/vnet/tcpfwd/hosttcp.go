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
	listener    net.Listener
	connectAddr tcpip.FullAddress
	gatewayAddr tcpip.Address
	stack       *stack.Stack
	nicId       tcpip.NICID
}

func StartTcpHostForward(s *stack.Stack, nicId tcpip.NICID, gatewayAddr string, listenAddr string, connectAddr string) error {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	connectAddrPort, err := netip.ParseAddrPort(connectAddr)
	if err != nil {
		return err
	}

	f := &TcpHostForwarder{
		listener: listener,
		connectAddr: tcpip.FullAddress{
			NIC:  nicId,
			Addr: tcpip.Address(connectAddrPort.Addr().AsSlice()),
			Port: uint16(connectAddrPort.Port()),
		},
		gatewayAddr: netutil.ParseTcpipAddress(gatewayAddr),
		stack:       s,
		nicId:       nicId,
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
	if remoteAddr.IP.To4() == nil {
		proto = ipv6.ProtocolNumber
	}

	// Spoof source address
	var srcAddr tcpip.Address
	if remoteAddr.IP.IsLoopback() {
		// We can't spoof loopback. Look up the host's default address.
		if proto == ipv4.ProtocolNumber {
			srcAddr = tcpip.Address(netutil.GetDefaultAddress4())
		} else {
			srcAddr = tcpip.Address(netutil.GetDefaultAddress6())
		}

		// Fallback = gateway (i.e. if airplane mode)
		if srcAddr == "" {
			srcAddr = f.gatewayAddr
		}
	} else {
		srcAddr = tcpip.Address(remoteAddr.IP)
	}

	virtSrcAddr := tcpip.FullAddress{
		NIC:  f.nicId,
		Addr: srcAddr,
		Port: uint16(remoteAddr.Port),
	}

	fmt.Println("dial from", virtSrcAddr, "to", f.connectAddr, "with proto", proto, "and timeout", connectTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(connectTimeout))
	defer cancel()
	virtConn, err := gonet.DialTCPWithBind(ctx, f.stack, virtSrcAddr, f.connectAddr, proto)
	if err != nil {
		return
	}
	defer virtConn.Close()

	pump2(conn, virtConn)
}

func (f *TcpHostForwarder) Stop() {
	f.listener.Close()
}
