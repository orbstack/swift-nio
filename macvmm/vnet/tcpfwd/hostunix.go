package tcpfwd

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type UnixTcpHostForwarder struct {
	listener    net.Listener
	connectAddr tcpip.FullAddress
	stack       *stack.Stack
	nicID       tcpip.NICID
}

func StartUnixTcpHostForward(s *stack.Stack, nicID tcpip.NICID, listenAddr, connectAddr string) error {
	listener, err := net.Listen("unix", listenAddr)
	if err != nil {
		return err
	}

	connectAddrPort, err := netip.ParseAddrPort(connectAddr)
	if err != nil {
		return err
	}

	f := &UnixTcpHostForwarder{
		listener: listener,
		connectAddr: tcpip.FullAddress{
			NIC:  nicID,
			Addr: tcpip.Address(connectAddrPort.Addr().AsSlice()),
			Port: uint16(connectAddrPort.Port()),
		},
		stack: s,
		nicID: nicID,
	}

	go f.listen()
	return nil
}

func (f *UnixTcpHostForwarder) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *UnixTcpHostForwarder) handleConn(conn net.Conn) {
	defer conn.Close()

	proto := ipv4.ProtocolNumber
	if f.connectAddr.Addr.To4() == "" {
		proto = ipv6.ProtocolNumber
	}

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(tcpConnectTimeout))
	defer cancel()
	virtConn, err := gonet.DialContextTCP(ctx, f.stack, f.connectAddr, proto)
	if err != nil {
		return
	}
	defer virtConn.Close()

	pump2(conn.(*net.UnixConn), virtConn)
}

func (f *UnixTcpHostForwarder) Stop() {
	f.listener.Close()
}
