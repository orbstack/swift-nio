package ntpsrv

import (
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/macvmgr/vnet/gonet"
	ntpserver "github.com/orbstack/macvirt/macvmgr/vnet/services/ntp/internal"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func ListenNTP(s *stack.Stack, address tcpip.Address) error {
	conn, err := gonet.DialUDP(s, &tcpip.FullAddress{
		Addr: address,
		Port: ports.ServiceNTP,
	}, nil, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	server := ntpserver.Server{PacketConn: conn}
	go server.Serve()
	return nil
}
