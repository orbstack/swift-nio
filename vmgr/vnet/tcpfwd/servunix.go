package tcpfwd

import (
	"net"
	"sync"

	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

type UnixNATForward struct {
	listener    net.Listener
	mu          sync.Mutex
	connectAddr string
	isSshAgent  bool
}

func ListenUnixNATForward(s *stack.Stack, listenAddr tcpip.FullAddress, connectAddr string, isSshAgent bool) (*UnixNATForward, error) {
	listener, err := gonet.ListenTCP(s, listenAddr, ipv4.ProtocolNumber)
	if err != nil {
		return nil, err
	}

	f := &UnixNATForward{
		listener:    listener,
		connectAddr: connectAddr,
		isSshAgent:  isSshAgent,
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

func (f *UnixNATForward) SetAddr(addr string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.connectAddr = addr
}

func (f *UnixNATForward) handleConn(conn net.Conn) {
	defer conn.Close()

	f.mu.Lock()
	connectAddr := f.connectAddr
	f.mu.Unlock()
	if connectAddr == "" {
		return
	}

	unixConn, err := net.Dial("unix", connectAddr)
	if err != nil {
		logrus.WithError(err).WithField("addr", connectAddr).Error("unix-nat forward: dial failed")
		return
	}
	defer unixConn.Close()

	if f.isSshAgent {
		pump2SshAgent(unixConn.(*net.UnixConn), conn.(*gonet.TCPConn))
	} else {
		pump2SpUnixGv(unixConn.(*net.UnixConn), conn.(*gonet.TCPConn))
	}
}

func (f *UnixNATForward) Close() error {
	return f.listener.Close()
}
