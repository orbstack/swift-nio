package network

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/kdrag0n/macvirt/macvmm/network/dgramlink"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const (
	subnet       = "172.30.30"
	gatewayIP    = subnet + ".1"
	gvnetMtu     = 65520
	guestSshAddr = subnet + ".3:22"

	subnet6 = "fc00:96dc:7096:1d21::"
)

func StartGvnetPair() (file *os.File, err error) {
	return runGvnetDgramPair()
}

func runGvnetDgramPair() (*os.File, error) {
	file0, fd1, err := makeUnixDgramPair()
	if err != nil {
		return nil, err
	}

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
	})

	macAddr, err := tcpip.ParseMACAddress("24:d2:f4:58:34:d7")
	if err != nil {
		return nil, err
	}

	endpoint, err := dgramlink.New(&dgramlink.Options{
		FDs:            []int{fd1},
		MTU:            gvnetMtu,
		EthernetHeader: true,
		Address:        macAddr,
		// no need for GSO when our MTU is so high. 16 -> 17 Gbps
		// GSOMaxSize:         gvnetMtu,
		GvisorGSOEnabled:   false,
		PacketDispatchMode: dgramlink.Readv,
		TXChecksumOffload:  true,
		RXChecksumOffload:  true,
	})
	if err != nil {
		return nil, err
	}

	// _ = os.Remove("gv.pcap")
	// f, err := os.Create("gv.pcap")
	//ep2, err := sniffer.NewWithWriter(endpoint, f, 2147483647)

	if err := s.CreateNIC(1, endpoint); err != nil {
		return nil, errors.New(err.String())
	}

	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.Address(net.ParseIP(gatewayIP).To4()).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv6.ProtocolNumber,
		AddressWithPrefix: tcpip.Address(net.ParseIP(subnet6 + "1").To16()).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}

	if err := s.SetSpoofing(1, true); err != nil {
		return nil, errors.New(err.String())
	}
	// Accept all packets so we can forward them
	if err := s.SetPromiscuousMode(1, true); err != nil {
		return nil, errors.New(err.String())
	}

	_, ipSubnet4, err := net.ParseCIDR(subnet + ".0/24")
	if err != nil {
		return nil, err
	}
	subnet4, err := tcpip.NewSubnet(tcpip.Address(ipSubnet4.IP.To4()), tcpip.AddressMask(ipSubnet4.Mask))
	if err != nil {
		return nil, err
	}

	_, ipSubnet6, err := net.ParseCIDR(subnet6 + "0/64")
	if err != nil {
		return nil, err
	}
	subnet6, err := tcpip.NewSubnet(tcpip.Address(ipSubnet6.IP.To16()), tcpip.AddressMask(ipSubnet6.Mask))
	if err != nil {
		return nil, err
	}
	s.SetRouteTable([]tcpip.Route{
		{
			Destination: subnet4,
			NIC:         1,
		},
		{
			Destination: subnet6,
			NIC:         1,
		},
	})

	// Performance
	{
		opt := tcpip.TCPSACKEnabled(true)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, errors.New(err.String())
		}
	}

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

	// TODO: pmtu, nagle's algorithm, buffer sizes
	// {
	// 	opt := tcpip.TCPDelayEnabled(false)
	// 	if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
	// 		return nil, errors.New(err.String())
	// 	}
	// }

	// {
	// 	opt := tcpip.TCPReceiveBufferSizeRangeOption{Min: 1, Default: 2 * 1024 * 1024, Max: 2 * 1024 * 1024}
	// 	s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
	// }
	// {
	// 	opt := tcpip.TCPSendBufferSizeRangeOption{Min: 1, Default: 2 * 1024 * 1024, Max: 2 * 1024 * 1024}
	// 	s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt)
	// }

	var natLock sync.Mutex
	tcpForwarder := newTcpForwarder(s, nil, &natLock)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		// println("tcp pkt")
		return tcpForwarder.HandlePacket(id, pkt)
	})
	// dbgf := tcp.NewForwarder(s, 0, 10, func(r *tcp.ForwarderRequest) {
	// 	println("got req")
	// 	r.Complete(true)
	// })
	// s.SetTransportProtocolHandler(tcp.ProtocolNumber, dbgf.HandlePacket)
	udpForwarder := newUdpForwarder(s, nil, &natLock)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)

	s.SetTransportProtocolHandler(icmp.ProtocolNumber4, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		fmt.Println("icmp4 id", id, "pkt", pkt)
		//pkt.RXChecksumValidated
		return true
	})

	s.SetTransportProtocolHandler(icmp.ProtocolNumber6, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		fmt.Println("icmp6 id", id, "pkt", pkt)
		return true
	})

	// TODO close the file eventually
	return file0, nil
}
