package ntpsrv

import (
	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	ntpserver "github.com/kdrag0n/macvirt/macvmm/vnet/services/ntp/internal"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	NTPPort = 123
)

func ListenNTP(s *stack.Stack, address tcpip.Address) error {
	conn, err := gonet.DialUDP(s, &tcpip.FullAddress{
		Addr: address,
		Port: NTPPort,
	}, nil, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	server := ntpserver.Server{PacketConn: conn}
	go server.Serve()
	return nil
}
