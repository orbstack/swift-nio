package main

import (
	"net"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/netconf"
	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
)

type HostServiceProxy struct {
	listener    net.Listener
	connectAddr *net.TCPAddr
}

func NewHostServiceProxy(unixPath string, port int, socketUidGid int) (*HostServiceProxy, error) {
	_ = os.Remove(unixPath)

	listener, err := net.Listen("unix", unixPath)
	if err != nil {
		return nil, err
	}

	// security: chmod 600 and chown to default user uid/gid
	err = os.Chmod(unixPath, 0600)
	if err != nil {
		return nil, err
	}

	err = os.Chown(unixPath, socketUidGid, socketUidGid)
	if err != nil {
		return nil, err
	}

	return &HostServiceProxy{
		listener:    listener,
		connectAddr: &net.TCPAddr{IP: net.ParseIP(netconf.SecureSvcIP4), Port: port},
	}, nil
}

func RunHostServiceProxy(unixPath string, port int, socketUidGid int) error {
	proxy, err := NewHostServiceProxy(unixPath, port, socketUidGid)
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

			extConn, err := net.DialTCP("tcp4", nil, p.connectAddr)
			if err != nil {
				return
			}
			defer extConn.Close()

			tcpfwd.Pump2SpTcpUnix(extConn, conn.(*net.UnixConn))
		}(conn)
	}
}

func (p *HostServiceProxy) Close() error {
	return p.listener.Close()
}
