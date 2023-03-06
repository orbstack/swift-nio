package tcpfwd

import (
	"fmt"
	"net"
	"sync"
	"syscall"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/sockets"
	"github.com/sirupsen/logrus"
)

type StreamVsockHostForward struct {
	listener        net.Listener
	dialer          func() (net.Conn, error)
	requireLoopback bool

	connsMu sync.Mutex
	conns   map[net.Conn]struct{}
}

func StartTcpVsockHostForward(listenAddr string, dialer func() (net.Conn, error)) (*StreamVsockHostForward, error) {
	listener, requireLoopback, err := ListenTCP(listenAddr)
	if err != nil {
		return nil, err
	}

	f := &StreamVsockHostForward{
		listener:        listener,
		dialer:          dialer,
		requireLoopback: requireLoopback,
		conns:           make(map[net.Conn]struct{}),
	}

	go f.listen()
	return f, nil
}

func StartUnixVsockHostForward(listenAddr string, dialer func() (net.Conn, error)) (*StreamVsockHostForward, error) {
	listener, err := net.Listen("unix", listenAddr)
	if err != nil {
		return nil, err
	}

	f := &StreamVsockHostForward{
		listener:        listener,
		dialer:          dialer,
		requireLoopback: false,
		conns:           make(map[net.Conn]struct{}),
	}

	go f.listen()
	return f, nil
}

func (f *StreamVsockHostForward) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *StreamVsockHostForward) handleConn(conn net.Conn) {
	f.connsMu.Lock()
	f.conns[conn] = struct{}{}
	f.connsMu.Unlock()

	defer conn.Close()
	defer func() {
		f.connsMu.Lock()
		delete(f.conns, conn)
		f.connsMu.Unlock()
	}()

	// Check remote address if using 0.0.0.0 to bypass privileged ports for loopback
	if remoteAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		if f.requireLoopback && !remoteAddr.IP.IsLoopback() {
			logrus.Debug("rejecting connection from non-loopback address ", remoteAddr)
			return
		}
	}

	virtConn, err := f.dialer()
	if err != nil {
		logrus.WithError(err).Error("host-vsock forward: dial failed")
		return
	}
	defer virtConn.Close()

	// NFS tuning (we only use this proxy for NFS now)
	// TODO: make this configurable
	rawConn, err := virtConn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return
	}
	rawConn.Control(func(fd uintptr) {
		err := sockets.SetLargeBuffers(int(fd))
		if err != nil {
			logrus.Error("failed to set buffers ", err)
		}
	})

	rawConn, _ = conn.(syscall.Conn).SyscallConn()
	if err != nil {
		return
	}
	rawConn.Control(func(fd uintptr) {
		err := sockets.SetLargeBuffers(int(fd))
		if err != nil {
			logrus.Error("failed to set buffers ", err)
		}
	})

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		err = setExtNodelay(tcpConn, 0) // vsock port is not considered
		if err != nil {
			logrus.Errorf("set ext opts failed ", err)
			return
		}
	}

	pump2(conn.(FullDuplexConn), virtConn.(*net.UnixConn))
}

func (f *StreamVsockHostForward) Close() error {
	f.connsMu.Lock()
	defer f.connsMu.Unlock()

	f.listener.Close()

	for conn := range f.conns {
		fmt.Println("close conn", conn)
		conn.Close()
	}

	return nil
}
