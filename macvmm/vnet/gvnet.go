package vnet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/kdrag0n/macvirt/macvmm/conf"
	"github.com/kdrag0n/macvirt/macvmm/vclient"
	"github.com/kdrag0n/macvirt/macvmm/vnet/dgramlink"
	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmm/vnet/icmpfwd"
	"github.com/kdrag0n/macvirt/macvmm/vnet/netutil"
	dnssrv "github.com/kdrag0n/macvirt/macvmm/vnet/services/dns"
	hcsrv "github.com/kdrag0n/macvirt/macvmm/vnet/services/hcontrol"
	ntpsrv "github.com/kdrag0n/macvirt/macvmm/vnet/services/ntp"
	sftpsrv "github.com/kdrag0n/macvirt/macvmm/vnet/services/sftp"
	"github.com/kdrag0n/macvirt/macvmm/vnet/tcpfwd"
	"github.com/kdrag0n/macvirt/macvmm/vnet/udpfwd"
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
	capturePcap  = false
	nicId        = 1

	subnet4     = "172.30.30"
	gatewayIP4  = subnet4 + ".1"
	guestIP4    = subnet4 + ".2"
	servicesIP4 = subnet4 + ".200"
	hostNatIP4  = subnet4 + ".254"

	subnet6    = "fc00:96dc:7096:1d21:"
	gatewayIP6 = subnet6 + ":1"
	guestIP6   = subnet6 + ":2"
	hostNatIP6 = subnet6 + ":254"

	gatewayMac = "24:d2:f4:58:34:d7"

	runDns      = true
	runNtp      = true
	runHcontrol = true
	runSftp     = false // Android
)

var (
	// host -> guest
	HostForwardsToGuest = map[string]string{
		"tcp:127.0.0.1:" + str(conf.HostPortSSH):      "tcp:" + str(conf.GuestPortSSH),
		"tcp:127.0.0.1:" + str(conf.HostPortNFS):      "tcp:" + str(conf.GuestPortNFS),
		"udp:127.0.0.1:" + str(conf.HostPortNFS):      "udp:" + str(conf.GuestPortNFS),
		"tcp:127.0.0.1:" + str(conf.HostPortNFSVsock): "vsock:" + str(conf.GuestPortNFS),
		"unix:" + conf.DockerSocket():                 "tcp:" + str(conf.GuestPortDocker),
		"tcp:127.0.0.1:" + str(conf.HostPortDocker):   "tcp:" + str(conf.GuestPortDocker),
	}
	// guest -> host
	natFromGuest = map[string]string{
		hostNatIP4: "127.0.0.1",
		hostNatIP6: "::1",
	}
	staticDnsHosts = map[string]dnssrv.StaticHost{
		"host":              {IP4: hostNatIP4, IP6: hostNatIP6},
		"host.internal":     {IP4: hostNatIP4, IP6: hostNatIP6},
		"services":          {IP4: servicesIP4},
		"services.internal": {IP4: servicesIP4},
		"gateway":           {IP4: gatewayIP4, IP6: gatewayIP6},
		"gateway.internal":  {IP4: gatewayIP4, IP6: gatewayIP6},
	}
)

type Network struct {
	Stack       *stack.Stack
	NIC         tcpip.NICID
	VClient     *vclient.VClient
	VsockDialer func(uint32) (net.Conn, error)
	file0       *os.File
	fd1         int
}

func str(port int) string {
	return strconv.Itoa(port)
}

type NetOptions struct {
	MTU uint32
}

func StartGvnetPair(opts NetOptions) (*Network, *os.File, error) {
	return runGvnetDgramPair(opts)
}

func runGvnetDgramPair(opts NetOptions) (*Network, *os.File, error) {
	file0, fd1, err := makeUnixDgramPair()
	if err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}

	endpoint, err := dgramlink.New(&dgramlink.Options{
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

	if capturePcap {
		_ = os.Remove("gv.pcap")
		f, err := os.Create("gv.pcap")
		if err != nil {
			return nil, nil, err
		}
		endpoint, err = sniffer.NewWithWriter(endpoint, f, math.MaxUint32)
	}

	if err := s.CreateNIC(nicId, endpoint); err != nil {
		return nil, nil, errors.New(err.String())
	}

	if err := s.AddProtocolAddress(nicId, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(gatewayIP4).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, nil, errors.New(err.String())
	}
	if err := s.AddProtocolAddress(nicId, tcpip.ProtocolAddress{
		Protocol:          ipv6.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(gatewayIP6).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, nil, errors.New(err.String())
	}

	if err := s.SetSpoofing(nicId, true); err != nil {
		return nil, nil, errors.New(err.String())
	}
	// Accept all packets so we can forward them
	if err := s.SetPromiscuousMode(nicId, true); err != nil {
		return nil, nil, errors.New(err.String())
	}

	_, ipSubnet4, err := net.ParseCIDR(subnet4 + ".0/24")
	if err != nil {
		return nil, nil, err
	}
	subnet4, err := tcpip.NewSubnet(tcpip.Address(ipSubnet4.IP.To4()), tcpip.AddressMask(ipSubnet4.Mask))
	if err != nil {
		return nil, nil, err
	}

	_, ipSubnet6, err := net.ParseCIDR(subnet6 + ":0/64")
	if err != nil {
		return nil, nil, err
	}
	subnet6, err := tcpip.NewSubnet(tcpip.Address(ipSubnet6.IP.To16()), tcpip.AddressMask(ipSubnet6.Mask))
	if err != nil {
		return nil, nil, err
	}

	s.SetRouteTable([]tcpip.Route{
		{Destination: subnet4, NIC: nicId},
		{Destination: subnet6, NIC: nicId},
	})

	// Fix NFS panic: disable SACK. Technically we only need to disable RACK, but SACK without RACK just increases overhead.
	{
		opt := tcpip.TCPSACKEnabled(true)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, nil, errors.New(err.String())
		}
	}
	{
		opt := tcpip.TCPRecovery(tcpip.TCPRACKStaticReoWnd)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, nil, errors.New(err.String())
		}
	}
	/*
		{
			opt := tcpip.TCPDelayEnabled(false)
			if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
				return nil, nil, errors.New(err.String())
			}
		}
	*/

	// Performance
	{
		opt := tcpip.TCPModerateReceiveBufferOption(true)
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, nil, errors.New(err.String())
		}
	}

	{
		opt := tcpip.CongestionControlOption("cubic")
		if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
			return nil, nil, errors.New(err.String())
		}
	}

	// TODO: pmtu, nagle's algorithm, buffer sizes
	// {
	// 	opt := tcpip.TCPDelayEnabled(false)
	// 	if err := s.SetTransportProtocolOption(tcp.ProtocolNumber, &opt); err != nil {
	// 		return nil, nil, errors.New(err.String())
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

	// ICMP
	icmpFwd, err := icmpfwd.NewIcmpFwd(s, nicId, guestIP4, guestIP6)
	if err != nil {
		return nil, nil, err
	}
	go icmpFwd.ProxyRequests()
	icmpFwd.MonitorReplies()

	// Services
	startNetServices(s)

	// vcontrol client
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return gonet.DialContextTCP(ctx, s, tcpip.FullAddress{
				Addr: netutil.ParseTcpipAddress(guestIP4),
				Port: vclient.VcontrolPort,
			}, ipv4.ProtocolNumber)
		},
	}
	vc := vclient.NewClient(tr)

	// TODO logger
	log.SetTarget(log.GoogleEmitter{Writer: &log.Writer{Next: bytes.NewBufferString("")}})

	network := &Network{
		Stack:       s,
		NIC:         nicId,
		VClient:     vc,
		VsockDialer: nil,
		file0:       file0,
		fd1:         fd1,
	}
	return network, file0, nil
}

func startNetServices(s *stack.Stack) {
	addr := netutil.ParseTcpipAddress(servicesIP4)

	// DNS (53): using system resolver
	if runDns {
		err := dnssrv.ListenDNS(s, addr, staticDnsHosts)
		if err != nil {
			fmt.Printf("Failed to start DNS server: %v\n", err)
		}
	}

	// NTP (123): using system time
	if runNtp {
		err := ntpsrv.ListenNTP(s, addr)
		if err != nil {
			fmt.Printf("Failed to start NTP server: %v\n", err)
		}
	}

	// Host control (8300): HTTP API
	if runHcontrol {
		err := hcsrv.ListenHcontrol(s, addr)
		if err != nil {
			fmt.Printf("Failed to start host control server: %v\n", err)
		}
	}

	// SFTP (22323): Android file sharing
	if runSftp {
		err := sftpsrv.ListenSFTP(s, addr)
		if err != nil {
			fmt.Printf("Failed to start SFTP server: %v\n", err)
		}
	}
}

func (n *Network) Close() error {
	n.VClient.Close()
	n.Stack.Destroy()
	n.file0.Close()
	file1 := os.NewFile(uintptr(n.fd1), "fd1")
	file1.Close()
	return nil
}
