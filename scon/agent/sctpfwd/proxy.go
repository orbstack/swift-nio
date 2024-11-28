package sctpfwd

import (
	"net"

	"github.com/orbstack/macvirt/scon/agent/sctpfwd/sctplib"
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
		conn, err := p.listener.Accept()
		if err != nil {
			return err
		}

		go p.handleConn(conn)
	}
}

func (p *SCTPProxy) handleConn(conn net.Conn) {
	defer conn.Close()

	upstreamConn, err := sctplib.DialSCTP(p.upstreamAddr)
	if err != nil {
		return
	}
	defer upstreamConn.Close()

	pump2(conn.(*sctplib.SCTPConn), upstreamConn)
}

func (p *SCTPProxy) Close() error {
	return p.listener.Close()
}
