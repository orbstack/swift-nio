package network

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/kdrag0n/macvirt/macvmm/network/dgramlink"
	"github.com/kdrag0n/macvirt/macvmm/network/icmpfwd"
	"github.com/kdrag0n/macvirt/macvmm/network/netutil"
	"github.com/kdrag0n/macvirt/macvmm/network/tcpfwd"
	"github.com/kdrag0n/macvirt/macvmm/network/udpfwd"
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
	GvnetMtu  = 65520
	writePcap = false
	nicId     = 1

	subnet4    = "172.30.30"
	gatewayIP4 = subnet4 + ".1"
	guestIP4   = subnet4 + ".2"
	hostNatIP4 = subnet4 + ".254"

	subnet6    = "fc00:96dc:7096:1d21:"
	gatewayIP6 = subnet6 + ":1"
	guestIP6   = subnet6 + ":2"
	hostNatIP6 = subnet6 + ":254"

	gatewayMac = "24:d2:f4:58:34:d7"
)

var (
	// host -> guest
	hostForwardsToGuest = map[string]string{
		"127.0.0.1:2222":  guestIP4 + ":22",
		"[::1]:2222":      "[" + guestIP6 + "]:22",
		"127.0.0.1:62429": guestIP4 + ":2049",
	}
	// guest -> host
	natFromGuest = map[string]string{
		hostNatIP4: "127.0.0.1",
		hostNatIP6: "::1",
	}
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
		RawFactory:               raw.EndpointFactory{},
		AllowPacketEndpointWrite: true,
	})

	macAddr, err := tcpip.ParseMACAddress(gatewayMac)
	if err != nil {
		return nil, err
	}

	endpoint, err := dgramlink.New(&dgramlink.Options{
		FDs:            []int{fd1},
		MTU:            GvnetMtu,
		EthernetHeader: true,
		Address:        macAddr,
		// no need for GSO when our MTU is so high. 16 -> 17 Gbps
		// GSOMaxSize:         GvnetMtu,
		GvisorGSOEnabled:   false,
		PacketDispatchMode: dgramlink.Readv,
		TXChecksumOffload:  true,
		RXChecksumOffload:  true,
	})
	if err != nil {
		return nil, err
	}

	if writePcap {
		_ = os.Remove("gv.pcap")
		f, err := os.Create("gv.pcap")
		if err != nil {
			return nil, err
		}
		endpoint, err = sniffer.NewWithWriter(endpoint, f, math.MaxUint32)
	}

	if err := s.CreateNIC(nicId, endpoint); err != nil {
		return nil, errors.New(err.String())
	}

	if err := s.AddProtocolAddress(nicId, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(gatewayIP4).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.AddProtocolAddress(nicId, tcpip.ProtocolAddress{
		Protocol:          ipv6.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(gatewayIP6).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}

	if err := s.SetSpoofing(nicId, true); err != nil {
		return nil, errors.New(err.String())
	}
	// Accept all packets so we can forward them
	if err := s.SetPromiscuousMode(nicId, true); err != nil {
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
		{Destination: subnet4, NIC: nicId},
		{Destination: subnet6, NIC: nicId},
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

	// Build NAT table
	var natLock sync.RWMutex
	natTable := make(map[tcpip.Address]tcpip.Address)
	for virtIp, hostIp := range natFromGuest {
		natTable[netutil.ParseTcpipAddress(virtIp)] = netutil.ParseTcpipAddress(hostIp)
	}

	// Forwarders
	tcpForwarder := tcpfwd.NewTcpForwarder(s, natTable, &natLock)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)

	udpForwarder := udpfwd.NewUdpForwarder(s, natTable, &natLock)
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

	// ICMP
	icmpFwd, err := icmpfwd.NewIcmpFwd(s)
	if err != nil {
		return nil, err
	}
	go icmpFwd.ProxyRequests()
	go icmpFwd.MonitorReplies(endpoint)

	// Host forwards
	for listenAddr, connectAddr := range hostForwardsToGuest {
		gatewayAddr := gatewayIP4
		if strings.ContainsRune(connectAddr, '[') {
			gatewayAddr = gatewayIP6
		}
		err := tcpfwd.StartTcpHostForward(s, nicId, gatewayAddr, listenAddr, connectAddr)
		if err != nil {
			return nil, err
		}
	}

	// TODO close the file eventually
	return file0, nil
}
