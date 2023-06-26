package tcpfwd

import (
	"net"
	"syscall"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/sockets"
	"github.com/sirupsen/logrus"
)

type StreamVsockHostForward struct {
	listener        net.Listener
	dialer          func() (net.Conn, error)
	requireLoopback bool
	nfsMode         bool
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
		nfsMode:         true,
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
		nfsMode:         false,
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
	defer conn.Close()

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
	err = rawConn.Control(func(fd uintptr) {
		err := sockets.SetLargeBuffers(int(fd))
		if err != nil {
			logrus.Error("failed to set buffers ", err)
		}
	})
	if err != nil {
		return
	}

	rawConn, _ = conn.(syscall.Conn).SyscallConn()
	if err != nil {
		return
	}
	err = rawConn.Control(func(fd uintptr) {
		err := sockets.SetLargeBuffers(int(fd))
		if err != nil {
			logrus.Error("failed to set buffers ", err)
		}
	})
	if err != nil {
		return
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		otherPort := 0 // vsock port is not considered
		if f.nfsMode {
			otherPort = ports.GuestNFS
		}
		err = setExtNodelay(tcpConn, otherPort)
		if err != nil {
			logrus.WithError(err).Error("set ext opts failed")
			return
		}
	}

	// specialized fast paths
	virtUnixConn := virtConn.(*net.UnixConn)
	if hostTcpConn, ok := conn.(*net.TCPConn); ok {
		pump2SpTcpUnix(hostTcpConn, virtUnixConn)
	} else if hostUnixConn, ok := conn.(*net.UnixConn); ok {
		pump2SpUnixUnix(hostUnixConn, virtUnixConn)
	} else {
		pump2(conn.(FullDuplexConn), virtUnixConn)
	}
}

func (f *StreamVsockHostForward) TcpPort() int {
	if tcpAddr, ok := f.listener.Addr().(*net.TCPAddr); ok {
		return tcpAddr.Port
	}

	return 0
}

func (f *StreamVsockHostForward) Close() error {
	return f.listener.Close()
}
