package main

import (
	"net"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
)

type HostServiceProxy struct {
	listener    net.Listener
	connectAddr *net.TCPAddr
}

func NewHostServiceProxy(unixPath string, port int, uid int, gid int) (*HostServiceProxy, error) {
	// security: chmod 660 and chown to passed uid/gid
	listener, err := util.ListenUnixWithPerms(unixPath, 0660, uid, gid)
	if err != nil {
		return nil, err
	}

	return &HostServiceProxy{
		listener:    listener,
		connectAddr: &net.TCPAddr{IP: net.ParseIP(netconf.VnetSecureSvcIP4), Port: port},
	}, nil
}

func RunHostServiceProxy(unixPath string, port int, uid int, gid int) error {
	proxy, err := NewHostServiceProxy(unixPath, port, uid, gid)
	if err != nil {
		return err
	}

	return proxy.Serve()
}

func (p *HostServiceProxy) Serve() error {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return err
		}

		go func(conn net.Conn) {
			defer conn.Close()

			// TODO: isolated should be blocked
			extConn, err := netx.DialTCP("tcp4", nil, p.connectAddr)
			if err != nil {
				return
			}
			defer extConn.Close()

			tcppump.Pump2SpTcpUnix(extConn, conn.(*net.UnixConn))
		}(conn)
	}
}

func (p *HostServiceProxy) Close() error {
	return p.listener.Close()
}
