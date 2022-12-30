package tcpfwd

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/sockets"
	"github.com/sirupsen/logrus"
)

type TcpVsockHostForwarder struct {
	listener        net.Listener
	dialer          func() (net.Conn, error)
	requireLoopback bool
}

func StartTcpVsockHostForward(listenAddr string, dialer func() (net.Conn, error)) error {
	listener, requireLoopback, err := ListenTCP(listenAddr)
	if err != nil {
		return err
	}

	f := &TcpVsockHostForwarder{
		listener:        listener,
		dialer:          dialer,
		requireLoopback: requireLoopback,
	}

	go f.listen()
	return nil
}

func (f *TcpVsockHostForwarder) listen() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}

		go f.handleConn(conn)
	}
}

func (f *TcpVsockHostForwarder) handleConn(conn net.Conn) {
	defer conn.Close()

	// Check remote address if using 0.0.0.0 to bypass privileged ports for loopback
	remoteAddr := conn.RemoteAddr().(*net.TCPAddr)
	if f.requireLoopback && !remoteAddr.IP.IsLoopback() {
		logrus.Debug("rejecting connection from non-loopback address ", remoteAddr)
		return
	}

	virtConn, err := f.dialer()
	if err != nil {
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
			logrus.Error("failed to set large buffers ", err)
		}
	})

	rawConn, _ = conn.(*net.TCPConn).SyscallConn()
	if err != nil {
		return
	}
	rawConn.Control(func(fd uintptr) {
		err := sockets.SetLargeBuffers(int(fd))
		if err != nil {
			logrus.Error("failed to set large buffers ", err)
		}
	})

	err = setExtNodelay(conn.(*net.TCPConn), 0) // vsock port is not considered
	if err != nil {
		logrus.Errorf("set ext opts failed ", err)
		return
	}

	pump2(conn.(*net.TCPConn), virtConn.(*net.UnixConn))
}

func (f *TcpVsockHostForwarder) Stop() {
	f.listener.Close()
}
