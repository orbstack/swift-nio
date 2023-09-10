package tcpfwd

import (
	"net"

	"github.com/orbstack/macvirt/scon/agent/registry"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/sirupsen/logrus"
)

var (
	ipv4Loopback = net.IPv4(127, 0, 0, 1)
)

type TCPProxy struct {
	listener         net.Listener
	preferV6         bool
	port             uint16
	registry         *registry.LocalTCPRegistry
	forceDialIP      net.IP
	forceDialOtherIP net.IP
}

func NewTCPProxy(listener net.Listener, preferV6 bool, port uint16, registry *registry.LocalTCPRegistry, forceDialIP net.IP, forceDialOtherIP net.IP) *TCPProxy {
	return &TCPProxy{
		listener:         listener,
		preferV6:         preferV6,
		port:             port,
		registry:         registry,
		forceDialIP:      forceDialIP,
		forceDialOtherIP: forceDialOtherIP,
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
	// try bypass -> local registry
	if p.registry != nil && p.registry.TakeConn(p.port, conn) {
		logrus.WithField("port", p.port).Debug("bypassing local registry")
		return
	}

	defer conn.Close()

	// dial
	dialAddr := net.TCPAddr{
		Port: int(p.port),
	}
	var otherIP net.IP
	if p.preferV6 {
		dialAddr.IP = net.IPv6loopback
		otherIP = ipv4Loopback
	} else {
		dialAddr.IP = ipv4Loopback
		otherIP = net.IPv6loopback
	}
	if p.forceDialIP != nil {
		dialAddr.IP = p.forceDialIP
		if p.forceDialOtherIP != nil {
			otherIP = p.forceDialOtherIP
		} else {
			otherIP = nil
		}
	}

	dialConn, err := netx.DialTCP("tcp", nil, &dialAddr)
	if err != nil {
		logrus.WithError(err).Error("failed to dial local (1)")

		// if conn refused (i.e. no listener) but our proxy is still registered,
		// try dialing the other v4/v6 protocol
		if otherIP != nil {
			dialAddr.IP = otherIP
			logrus.WithField("dialAddr", dialAddr).Debug("retrying with other protocol")
			dialConn, err = netx.DialTCP("tcp", nil, &dialAddr)
			if err != nil {
				logrus.WithError(err).Error("failed to dial local (2)")
				return
			}
		} else {
			return
		}
	}
	defer dialConn.Close()

	// set TCP_NODELAY for localhost
	dialConn.SetNoDelay(true)

	Pump2SpTcpTcp(conn.(*net.TCPConn), dialConn)
}

func (p *TCPProxy) Close() error {
	return p.listener.Close()
}
