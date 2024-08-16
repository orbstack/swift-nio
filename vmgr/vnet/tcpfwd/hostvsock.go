package tcpfwd

import (
	"net"
	"syscall"

	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/vnet/sockets"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
)

type StreamVsockHostForward struct {
	listener        net.Listener
	dialer          func() (net.Conn, error)
	requireLoopback bool
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
	}

	go f.listen()
	return f, nil
}

func StartUnixVsockHostForward(listenAddr string, dialer func() (net.Conn, error)) (*StreamVsockHostForward, error) {
	listener, err := netx.ListenUnix(listenAddr)
	if err != nil {
		return nil, err
	}

	f := &StreamVsockHostForward{
		listener:        listener,
		dialer:          dialer,
		requireLoopback: false,
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

	rawConn, err = conn.(syscall.Conn).SyscallConn()
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

	// keep TCP_NODELAY on:
	// anything that uses vsock cares about latency

	// specialized fast paths
	virtUnixConn := virtConn.(*net.UnixConn)
	if hostTcpConn, ok := conn.(*net.TCPConn); ok {
		tcppump.Pump2SpTcpUnix(hostTcpConn, virtUnixConn)
	} else if hostUnixConn, ok := conn.(*net.UnixConn); ok {
		tcppump.Pump2SpUnixUnix(hostUnixConn, virtUnixConn)
	} else {
		tcppump.Pump2(conn.(tcppump.FullDuplexConn), virtUnixConn)
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
