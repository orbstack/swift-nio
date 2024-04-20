package tcpfwd

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/netutil"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type UnixTcpHostForward struct {
	listener    net.Listener
	connectAddr tcpip.FullAddress
	stack       *stack.Stack
	nicID       tcpip.NICID
}

func StartUnixTcpHostForward(s *stack.Stack, nicID tcpip.NICID, listenAddr, connectAddr string) (*UnixTcpHostForward, error) {
	listener, err := netx.ListenUnix(listenAddr)
	if err != nil {
		return nil, err
	}

	connectAddrPort, err := netip.ParseAddrPort(connectAddr)
	if err != nil {
		return nil, err
	}

	f := &UnixTcpHostForward{
		listener: listener,
		connectAddr: tcpip.FullAddress{
			NIC:  nicID,
			Addr: netutil.AddrFromNetip(connectAddrPort.Addr()),
			Port: uint16(connectAddrPort.Port()),
		},
		stack: s,
		nicID: nicID,
	}

	go f.listen()
	return f, nil
}

func (f *UnixTcpHostForward) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *UnixTcpHostForward) handleConn(conn net.Conn) {
	defer conn.Close()

	proto := ipv4.ProtocolNumber
	if f.connectAddr.Addr.To4() == (tcpip.Address{}) {
		proto = ipv6.ProtocolNumber
	}

	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(tcpConnectTimeout))
	defer cancel()
	virtConn, err := gonet.DialContextTCP(ctx, f.stack, f.connectAddr, proto)
	if err != nil {
		logrus.WithError(err).WithField("addr", f.connectAddr).Error("host-unix forward: dial failed")
		return
	}
	defer virtConn.Close()

	pump2SpUnixGv(conn.(*net.UnixConn), virtConn)
}

func (f *UnixTcpHostForward) Close() error {
	return f.listener.Close()
}
