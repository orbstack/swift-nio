package sftpsrv

import (
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/macvmgr/vnet/gonet"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func ListenSFTP(stack *stack.Stack, address tcpip.Address) error {
	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.ServiceSFTP,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				server, err := sftp.NewServer(conn, sftp.WithAllocator())
				if err != nil {
					logrus.Error("sftp.NewServer() =", err)
					return
				}
				defer server.Close()

				err = server.Serve()
				if err != nil {
					logrus.Error("server.Serve() =", err)
					return
				}
			}()
		}
	}()

	return nil
}
