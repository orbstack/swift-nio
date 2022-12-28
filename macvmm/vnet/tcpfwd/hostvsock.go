package tcpfwd

import (
	"fmt"
	"net"
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
		fmt.Println("rejecting connection from non-loopback address", remoteAddr)
		return
	}

	virtConn, err := f.dialer()
	if err != nil {
		return
	}
	defer virtConn.Close()

	pump2(conn, virtConn)
}

func (f *TcpVsockHostForwarder) Stop() {
	f.listener.Close()
}
