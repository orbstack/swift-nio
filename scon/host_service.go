package main

import (
	"net"
	"os"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
)

type HostServiceProxy struct {
	listener    net.Listener
	connectAddr *net.TCPAddr
}

func listenUnixWithPerms(path string, perms os.FileMode, uid, gid int) (net.Listener, error) {
	_ = os.Remove(path)

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	err = os.Chmod(path, perms)
	if err != nil {
		return nil, err
	}

	err = os.Chown(path, uid, gid)
	if err != nil {
		return nil, err
	}

	return listener, nil
}

func NewHostServiceProxy(unixPath string, port int, socketUidGid int) (*HostServiceProxy, error) {
	// security: chmod 600 and chown to default user uid/gid
	listener, err := listenUnixWithPerms(unixPath, 0600, socketUidGid, socketUidGid)
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

			// TODO: isolated should be blocked
			extConn, err := netx.DialTCP("tcp4", nil, p.connectAddr)
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
