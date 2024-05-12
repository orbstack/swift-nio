package netx

import (
	"net"
	"os"
)

func DialTCP(network string, laddr, raddr *net.TCPAddr) (*net.TCPConn, error) {
	conn, err := net.DialTCP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}

	// disable keepalive
	conn.SetKeepAlive(false)
	return conn, nil
}

func Dial(network, address string) (net.Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	// disable keepalive
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(false)
	}
	return conn, nil
}

type TCPListener struct {
	*net.TCPListener
}

func ListenTCP(network string, laddr *net.TCPAddr) (*TCPListener, error) {
	listener, err := net.ListenTCP(network, laddr)
	if err != nil {
		return nil, err
	}
	return &TCPListener{listener}, nil
}

func (l *TCPListener) Accept() (net.Conn, error) {
	conn, err := l.TCPListener.AcceptTCP()
	if err != nil {
		return nil, err
	}

	// disable keepalive
	conn.SetKeepAlive(false)
	return conn, nil
}

func Listen(network, address string) (net.Listener, error) {
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	if tcpListener, ok := listener.(*net.TCPListener); ok {
		return &TCPListener{tcpListener}, nil
	}
	return listener, nil
}

func ListenUnix(path string) (net.Listener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path})
	if err != nil {
		return nil, err
	}

	// vmgr runs with umask 0 for virtiofs purposes. default mode for unix bind is 0777
	// this is racy but we have no option. set it back to 0755 (~0022)
	err = os.Chmod(path, 0755)
	if err != nil {
		return nil, err
	}

	return listener, nil
}
