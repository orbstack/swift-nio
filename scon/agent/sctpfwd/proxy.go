package sctpfwd

import (
	"net"

	"github.com/orbstack/macvirt/scon/agent/sctpfwd/sctplib"
	"github.com/sirupsen/logrus"
)

type SCTPProxy struct {
	listener     net.Listener
	upstreamAddr *sctplib.SCTPAddr
}

func NewSCTPProxy(listener net.Listener, upstreamAddr *sctplib.SCTPAddr) *SCTPProxy {
	return &SCTPProxy{
		listener:     listener,
		upstreamAddr: upstreamAddr,
	}
}

func (p *SCTPProxy) Run() error {
	for {
		logrus.Debug("sctp proxy waiting for connection")
		conn, err := p.listener.Accept()
		if err != nil {
			logrus.Error("SCTP proxy accept failed: ", err)
			return err
		}

		logrus.Debug("sctp proxy accepted connection", conn)
		go p.handleConn(conn)
	}
}

func (p *SCTPProxy) handleConn(conn net.Conn) {
	logrus.Debug("sctp proxy handling connection", conn)
	defer conn.Close()

	logrus.Debug("sctp proxy dialing upstream", p.upstreamAddr)
	upstreamConn, err := sctplib.DialSCTP(p.upstreamAddr)
	if err != nil {
		logrus.Error("SCTP dial failed: ", err)
		return
	}
	defer upstreamConn.Close()

	logrus.Debug("sctp proxy pumping")
	pump2(conn.(*sctplib.SCTPConn), upstreamConn)
}

func (p *SCTPProxy) Close() error {
	logrus.Debug("sctp proxy closing")
	return p.listener.Close()
}
