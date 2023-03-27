package tcpfwd

import (
	"net"

	"github.com/sirupsen/logrus"
)

var (
	ipv4Loopback = net.IPv4(127, 0, 0, 1)
)

type TCPProxy struct {
	listener net.Listener
	isIPv6   bool
	port     uint16
}

func NewTCPProxy(listener net.Listener, isIPv6 bool, port uint16) *TCPProxy {
	return &TCPProxy{
		listener: listener,
		isIPv6:   isIPv6,
		port:     port,
	}
}

func (p *TCPProxy) Run() error {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return err
		}

		go p.handleConn(conn)
	}
}

func (p *TCPProxy) handleConn(conn net.Conn) {
	defer conn.Close()

	// dial
	dialAddr := net.TCPAddr{
		Port: int(p.port),
	}
	if p.isIPv6 {
		dialAddr.IP = net.IPv6loopback
	} else {
		dialAddr.IP = ipv4Loopback
	}

	dialConn, err := net.DialTCP("tcp", nil, &dialAddr)
	if err != nil {
		logrus.WithError(err).Error("failed to dial local (1)")

		// if conn refused (i.e. no listener) but our proxy is still registered,
		// try dialing the other v4/v6 protocol
		if p.isIPv6 {
			dialAddr.IP = ipv4Loopback
		} else {
			dialAddr.IP = net.IPv6loopback
		}
		logrus.WithField("dialAddr", dialAddr).Debug("retrying with other protocol")
		dialConn, err = net.DialTCP("tcp", nil, &dialAddr)
		if err != nil {
			logrus.WithError(err).Error("failed to dial local (2)")
			return
		}
	}
	defer dialConn.Close()

	// set TCP_NODELAY for localhost
	dialConn.SetNoDelay(true)

	Pump2(conn.(*net.TCPConn), dialConn)
}

func (p *TCPProxy) Close() error {
	return p.listener.Close()
}
