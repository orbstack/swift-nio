package tcpfwd

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/kdrag0n/macvirt/macvmm/network/gonet"
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
	stack       *stack.Stack
	nicId       tcpip.NICID
}

func StartTcpHostForward(s *stack.Stack, nicId tcpip.NICID, listenAddr string, connectAddr string) error {
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
		stack: s,
		nicId: nicId,
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

	// Spoof source address
	remoteAddr := conn.RemoteAddr().(*net.TCPAddr)
	virtSrcAddr := tcpip.FullAddress{
		NIC:  f.nicId,
		//Addr: tcpip.Address(remoteAddr.IP),
		Addr: tcpip.Address(net.ParseIP("172.30.30.1").To4()),
		Port: uint16(remoteAddr.Port),
	}

	proto := ipv4.ProtocolNumber
	if remoteAddr.IP.To16() != nil {
		proto = ipv6.ProtocolNumber
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
