package sftpsrv

import (
	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/pkg/sftp"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"k8s.io/klog/v2"
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
					klog.Error("sftp.NewServer() =", err)
					return
				}
				defer server.Close()

				err = server.Serve()
				if err != nil {
					klog.Error("server.Serve() =", err)
					return
				}
			}()
		}
	}()

	return nil
}
