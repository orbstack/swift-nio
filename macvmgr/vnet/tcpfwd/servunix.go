package tcpfwd

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type UnixNATForward struct {
	listener    net.Listener
	connectAddr string
}

func ListenUnixNATForward(s *stack.Stack, listenAddr tcpip.Address, connectAddr string) (*UnixNATForward, error) {
	listener, err := gonet.ListenTCP(s, tcpip.FullAddress{
		Addr: listenAddr,
		Port: ports.SecureSvcHostSSHAgent,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return nil, err
	}

	f := &UnixNATForward{
		listener:    listener,
		connectAddr: connectAddr,
	}

	go f.listen()
	return f, nil
}

func (f *UnixNATForward) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *UnixNATForward) handleConn(conn net.Conn) {
	defer conn.Close()

	unixConn, err := net.Dial("unix", f.connectAddr)
	if err != nil {
		logrus.WithError(err).WithField("addr", f.connectAddr).Error("unix-nat forward: dial failed")
		return
	}
	defer unixConn.Close()

	pump2(conn.(*gonet.TCPConn), unixConn.(*net.UnixConn))
}

func (f *UnixNATForward) Close() error {
	return f.listener.Close()
}
