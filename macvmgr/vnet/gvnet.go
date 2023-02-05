package vnet

import (
	"bytes"
	"errors"
	"math"
	"net"
	"os"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/dgramlink"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/icmpfwd"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/qemulink"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/udpfwd"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/link/sniffer"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/raw"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const (
	PreferredMtu = 65520
	capturePcap  = true
	nicID        = 1

	subnet4      = "172.30.30"
	GatewayIP4   = subnet4 + ".1"
	GuestIP4     = subnet4 + ".2"
	ServicesIP4  = subnet4 + ".200"
	SecureSvcIP4 = subnet4 + ".201"
	HostNatIP4   = subnet4 + ".254"

	subnet6    = "fc00:96dc:7096:1d21:"
	GatewayIP6 = subnet6 + ":1"
	GuestIP6   = subnet6 + ":2"
	HostNatIP6 = subnet6 + ":254"

	gatewayMac = "24:d2:f4:58:34:d7"
)

var (
	// guest -> host
	natFromGuest = map[string]string{
		HostNatIP4: "127.0.0.1",
		HostNatIP6: "::1",
	}
)

type Network struct {
	Stack       *stack.Stack
	NIC         tcpip.NICID
	VsockDialer func(uint32) (net.Conn, error)
	ICMP        *icmpfwd.IcmpFwd
	NatTable    map[tcpip.Address]tcpip.Address
	GuestAddr4  tcpip.Address
	GuestAddr6  tcpip.Address
	// mapped by host side. guest side can be duplicated
	hostForwards  map[string]HostForward
	hostForwardMu sync.Mutex
	file0         *os.File
	fd1           int
}

type NetOptions struct {
	MTU uint32
}

func StartUnixgramPair(opts NetOptions) (*Network, *os.File, error) {
	file0, fd1, err := NewUnixgramPair()
	if err != nil {
		return nil, nil, err
	}

	macAddr, err := tcpip.ParseMACAddress(gatewayMac)
	if err != nil {
		return nil, nil, err
	}

	nicEp, err := dgramlink.New(&dgramlink.Options{
		FDs:            []int{fd1},
		MTU:            opts.MTU,
		EthernetHeader: true,
		Address:        macAddr,
		// no need for GSO when our MTU is so high. 16 -> 17 Gbps
		// GSOMaxSize:         opts.MTU,
		GvisorGSOEnabled:   false,
		PacketDispatchMode: dgramlink.Readv,
		TXChecksumOffload:  true,
		RXChecksumOffload:  true,
	})
	if err != nil {
		return nil, nil, err
	}

	network, err := startNet(opts, nicEp)
	if err != nil {
		return nil, nil, err
	}

	network.file0 = file0
	network.fd1 = fd1
	return network, file0, nil
}

func StartQemuFd(opts NetOptions, file *os.File) (*Network, error) {
	macAddr, err := tcpip.ParseMACAddress(gatewayMac)
	if err != nil {
		return nil, err
	}

	nicEp, err := qemulink.New(file, macAddr)
	if err != nil {
		return nil, err
	}

	network, err := startNet(opts, nicEp)
	if err != nil {
		return nil, err
	}

	network.file0 = file
	return network, nil
}

func startNet(opts NetOptions, nicEp stack.LinkEndpoint) (*Network, error) {
	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
			arp.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
			icmp.NewProtocol4,
			icmp.NewProtocol6,
		},
		RawFactory:               raw.EndpointFactory{},
		AllowPacketEndpointWrite: true,
	})

	if capturePcap {
		_ = os.Remove("gv.pcap")
		f, err := os.Create("gv.pcap")
		if err != nil {
			return nil, err
		}
		nicEp, err = sniffer.NewWithWriter(nicEp, f, math.MaxUint32)
		if err != nil {
			return nil, err
		}
	}

	if err := s.CreateNIC(nicID, nicEp); err != nil {
		return nil, errors.New(err.String())
	}

	if err := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(GatewayIP4).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ipv6.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(GatewayIP6).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}

	if err := s.SetSpoofing(nicID, true); err != nil {
		return nil, errors.New(err.String())
	}
	// Accept all packets so we can forward them
	if err := s.SetPromiscuousMode(nicID, true); err != nil {
		return nil, errors.New(err.String())
	}

	_, ipSubnet4, err := net.ParseCIDR(subnet4 + ".0/24")
	if err != nil {
		return nil, err
	}
	subnet4, err := tcpip.NewSubnet(tcpip.Address(ipSubnet4.IP.To4()), tcpip.AddressMask(ipSubnet4.Mask))
	if err != nil {
		return nil, err
	}

	_, ipSubnet6, err := net.ParseCIDR(subnet6 + ":0/64")
	if err != nil {
		return nil, err
	}
	subnet6, err := tcpip.NewSubnet(tcpip.Address(ipSubnet6.IP.To16()), tcpip.AddressMask(ipSubnet6.Mask))
	if err != nil {
		return nil, err
	}

	s.SetRouteTable([]tcpip.Route{
		{Destination: subnet4, NIC: nicID},
		{Destination: subnet6, NIC: nicID},
	})

	// Performance. Not actually causing NFS panics
	{
		opt := tcpip.TCPSACKEnabled(true)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, errors.New(err.String())
		}
	}
	// Our network link is pretty much perfect. We control this on the external end instead
	{
		opt := tcpip.TCPDelayEnabled(false)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, errors.New(err.String())
		}
	}

	// Performance
	{
		opt := tcpip.TCPModerateReceiveBufferOption(true)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, errors.New(err.String())
		}
	}

	{
		opt := tcpip.CongestionControlOption("cubic")
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, errors.New(err.String())
		}
	}

	// TODO: buffer sizes
	// {
	// 	opt := tcpip.TCPReceiveBufferSizeRangeOption{Min: 1, Default: 2 * 1024 * 1024, Max: 2 * 1024 * 1024}
	// 	s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
	// }
	// {
	// 	opt := tcpip.TCPSendBufferSizeRangeOption{Min: 1, Default: 2 * 1024 * 1024, Max: 2 * 1024 * 1024}
	// 	s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
	// }

	// ICMP, used by forwarders
	guestAddr4 := netutil.ParseTcpipAddress(GuestIP4)
	guestAddr6 := netutil.ParseTcpipAddress(GuestIP6)
	gatewayAddr4 := netutil.ParseTcpipAddress(GatewayIP4)
	gatewayAddr6 := netutil.ParseTcpipAddress(GatewayIP6)
	icmpFwd, err := icmpfwd.NewIcmpFwd(s, nicID, guestAddr4, guestAddr6, gatewayAddr4, gatewayAddr6)
	if err != nil {
		return nil, err
	}
	go icmpFwd.ProxyRequests()
	icmpFwd.MonitorReplies()

	// Build NAT table
	var natLock sync.RWMutex
	natTable := make(map[tcpip.Address]tcpip.Address)
	for virtIp, hostIp := range natFromGuest {
		natTable[netutil.ParseTcpipAddress(virtIp)] = netutil.ParseTcpipAddress(hostIp)
	}

	// Forwarders
	tcpForwarder := tcpfwd.NewTcpForwarder(s, natTable, &natLock, icmpFwd)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)

	udpForwarder := udpfwd.NewUdpForwarder(s, natTable, &natLock, icmpFwd)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)

	// Silence gvisor logs
	log.SetTarget(log.GoogleEmitter{Writer: &log.Writer{Next: bytes.NewBufferString("")}})

	network := &Network{
		Stack:        s,
		NIC:          nicID,
		VsockDialer:  nil,
		ICMP:         icmpFwd,
		NatTable:     natTable,
		GuestAddr4:   guestAddr4,
		GuestAddr6:   guestAddr6,
		hostForwards: make(map[string]HostForward),
		file0:        nil,
		fd1:          -1,
	}

	return network, nil
}

func (n *Network) Close() error {
	n.Stack.Destroy()
	if n.file0 != nil {
		n.file0.Close()
	}
	if n.fd1 != -1 {
		file1 := os.NewFile(uintptr(n.fd1), "fd1")
		file1.Close()
	}
	return nil
}
