package sftpsrv

import (
	"log"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/pkg/sftp"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	SFTPPort = 22323
)

func ListenSFTP(stack *stack.Stack, address tcpip.Address) error {
	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: SFTPPort,
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
					log.Println("sftp.NewServer() =", err)
					return
				}
				defer server.Close()

				err = server.Serve()
				if err != nil {
					log.Println("server.Serve() =", err)
					return
				}
			}()
		}
	}()

	return nil
}
