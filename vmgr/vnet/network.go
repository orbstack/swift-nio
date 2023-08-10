package vnet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sync"
	"time"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/vnet/bridge"
	"github.com/orbstack/macvirt/vmgr/vnet/dglink"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/icmpfwd"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/netutil"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd"
	"github.com/orbstack/macvirt/vmgr/vnet/udpfwd"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
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
	PreferredMTU = 65535
	// fallback on macOS 12
	BaseMTU     = 1500
	capturePcap = false
	nicID       = 1

	// TODO event based startup
	guestDialRetryInterval = 250 * time.Millisecond
	guestDialRetryTimeout  = 15 * time.Second
)

type HostBridge interface {
	io.Closer
}

type Network struct {
	Stack   *stack.Stack
	NIC     tcpip.NICID
	LinkMTU uint32

	file0 *os.File
	fd1   int

	VsockDialer func(uint32) (net.Conn, error)

	icmp       *icmpfwd.IcmpFwd
	NatTable   map[tcpip.Address]tcpip.Address
	GuestAddr4 tcpip.Address
	GuestAddr6 tcpip.Address

	Proxy *tcpfwd.ProxyManager

	// mapped by host side. guest side can be duplicated
	hostForwards  map[string]HostForward
	hostForwardMu sync.Mutex

	// bridges
	hostBridgeMu           sync.Mutex
	hostBridgeFds          []int
	hostBridges            []HostBridge
	bridgeRouteMon         *bridge.RouteMon
	vlanRouter             *vzf.VlanRouter
	vlanIndices            map[sgtypes.DockerBridgeConfig]int
	disableMachineBridgeV4 bool

	// services we need references to
	DockerRemoteCtxForward *tcpfwd.UnixNATForward
}

type NetOptions struct {
	LinkMTU uint32
}

func StartUnixgramPair(opts NetOptions) (*Network, *os.File, error) {
	file0, fd1, err := NewUnixgramPair()
	if err != nil {
		return nil, nil, err
	}

	macAddr, err := tcpip.ParseMACAddress(netconf.GatewayMAC)
	if err != nil {
		return nil, nil, err
	}

	linkOpts := dglink.Options{
		FDs:            []int{fd1},
		MTU:            opts.LinkMTU,
		EthernetHeader: true,
		Address:        macAddr,
		// only enable GSO for high MTU
		GSOMaxSize: 0,
		// if GSO is enabled, we add a virtio_net_hdr
		GvisorGSOEnabled:   false,
		PacketDispatchMode: dglink.RecvMMsg,
		TXChecksumOffload:  true,
		RXChecksumOffload:  true,
	}

	// for high MTU, add double virtio_net_hdr for GSO/TSO metadata
	// for low MTU, don't touch it or we'd end up with 1490 MTU.
	// (no point anyway because there's no GSO to do at 1500)
	//
	// this causes asymmetric MTU:
	// - guest -> host: 65535 (TSO from 1500)
	// - host -> guest: 65517 (65535 - 10 (vnet_hdr) - 8 (ipv6 overhead))
	// but that's ok, because official MTU on the Linux side is 1500. 65535 is a TSO detail
	if opts.LinkMTU > BaseMTU {
		// IPv6 gets truncated without -8 bytes on GSOMaxSize. TODO: why?
		// also, we don't strictly need -8 on MTU, only GSOMaxSize. but just make it match to avoid issues
		linkOpts.MTU -= uint32(dglink.VirtioNetHdrSize + 8)
		// we use GSO *with* high MTU just to give Linux kernel the GSO/TSO metadata
		// so it can split for mtu-1500 bridges like Docker Compose
		linkOpts.GSOMaxSize = linkOpts.MTU
	}

	nicEp, err := dglink.New(&linkOpts)
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
		pcapPath := conf.MustExecutableDir() + "/gv.pcap"
		_ = os.Remove(pcapPath)
		f, err := os.Create(pcapPath)
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
		AddressWithPrefix: netutil.ParseTcpipAddress(netconf.GatewayIP4).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
		Protocol:          ipv6.ProtocolNumber,
		AddressWithPrefix: netutil.ParseTcpipAddress(netconf.GatewayIP6).WithPrefix(),
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

	_, ipSubnet4, err := net.ParseCIDR(netconf.Subnet4 + ".0/24")
	if err != nil {
		return nil, err
	}
	subnet4, err := tcpip.NewSubnet(tcpip.AddrFrom4Slice(ipSubnet4.IP.To4()), tcpip.MaskFromBytes(ipSubnet4.Mask))
	if err != nil {
		return nil, err
	}

	_, ipSubnet6, err := net.ParseCIDR(netconf.Subnet6 + ":0/64")
	if err != nil {
		return nil, err
	}
	subnet6, err := tcpip.NewSubnet(tcpip.AddrFrom16Slice(ipSubnet6.IP.To16()), tcpip.MaskFromBytes(ipSubnet6.Mask))
	if err != nil {
		return nil, err
	}

	s.SetRouteTable([]tcpip.Route{
		{Destination: subnet4, NIC: nicID},
		{Destination: subnet6, NIC: nicID},
	})

	// Disable SACK for performance
	// SACK causes high iperf3 retransmits through machine bridge/NAT (10-50/sec @ 65k MTU, 50-500/sec @ 1500 MTU + TSO)
	// gvisor SACK is broken: https://github.com/google/gvisor/issues/7406
	// TODO: why does veth and TSO affect it?
	{
		opt := tcpip.TCPSACKEnabled(false)
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

	// fix time_wait
	{
		opt := tcpip.TCPTimeWaitReuseOption(tcpip.TCPTimeWaitReuseGlobal)
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
	guestAddr4 := netutil.ParseTcpipAddress(netconf.GuestIP4)
	guestAddr6 := netutil.ParseTcpipAddress(netconf.GuestIP6)
	gatewayAddr4 := netutil.ParseTcpipAddress(netconf.GatewayIP4)
	gatewayAddr6 := netutil.ParseTcpipAddress(netconf.GatewayIP6)

	// add static neighbors so we don't need ARP (waste of CPU)
	guestMac, err := tcpip.ParseMACAddress(netconf.GuestMACVnet)
	if err != nil {
		return nil, err
	}
	if err := s.AddStaticNeighbor(nicID, ipv4.ProtocolNumber, guestAddr4, guestMac); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.AddStaticNeighbor(nicID, ipv6.ProtocolNumber, guestAddr6, guestMac); err != nil {
		return nil, errors.New(err.String())
	}

	icmpFwd, err := icmpfwd.NewIcmpFwd(s, nicID, guestAddr4, guestAddr6, gatewayAddr4, gatewayAddr6)
	if err != nil {
		return nil, err
	}
	go icmpFwd.ProxyRequests()
	icmpFwd.MonitorReplies()

	// Build NAT table
	hostNatIP4 := netutil.ParseTcpipAddress(netconf.HostNatIP4)
	hostNatIP6 := netutil.ParseTcpipAddress(netconf.HostNatIP6)

	// Forwarders
	tcpForwarder, proxyManager := tcpfwd.NewTcpForwarder(s, icmpFwd, hostNatIP4, hostNatIP6)
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpForwarder.HandlePacket)

	udpForwarder := udpfwd.NewUdpForwarder(s, icmpFwd, hostNatIP4, hostNatIP6)
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpForwarder.HandlePacket)

	// Silence gvisor logs
	log.SetTarget(log.GoogleEmitter{Writer: &log.Writer{Next: bytes.NewBufferString("")}})

	bridgeRouteMon, err := bridge.NewRouteMon()
	if err != nil {
		return nil, err
	}

	network := &Network{
		Stack:        s,
		NIC:          nicID,
		LinkMTU:      opts.LinkMTU,
		VsockDialer:  nil,
		icmp:         icmpFwd,
		GuestAddr4:   guestAddr4,
		GuestAddr6:   guestAddr6,
		Proxy:        proxyManager,
		hostForwards: make(map[string]HostForward),
		file0:        nil,
		fd1:          -1,

		bridgeRouteMon: bridgeRouteMon,
	}

	return network, nil
}

func (n *Network) stopForwards() {
	n.hostForwardMu.Lock()
	defer n.hostForwardMu.Unlock()

	for spec, f := range n.hostForwards {
		logrus.WithField("spec", spec).Debug("closing forward")
		err := f.Close()
		if err != nil {
			logrus.WithError(err).WithField("spec", spec).Warn("failed to close forward")
		}
		delete(n.hostForwards, spec)
	}
}

func (n *Network) Close() error {
	n.stopForwards()
	n.stopHostBridges()
	n.icmp.Close()
	if n.Proxy != nil {
		n.Proxy.Close()
	}
	// destroy waits and blocks, but this sometiems does too...
	//n.Stack.Close()
	if n.file0 != nil {
		n.file0.Close()
	}
	if n.fd1 != -1 {
		unix.Close(n.fd1)
	}
	return nil
}

func (n *Network) DialGuestTCP(port uint16) (net.Conn, error) {
	return gonet.DialTCP(n.Stack, tcpip.FullAddress{
		NIC:  n.NIC,
		Addr: n.GuestAddr4,
		Port: port,
	}, ipv4.ProtocolNumber)
}

func (n *Network) DialGuestTCPRetry(port uint16) (net.Conn, error) {
	start := time.Now()
	for {
		conn, err := n.DialGuestTCP(port)
		if err == nil {
			return conn, nil
		}
		if time.Since(start) > guestDialRetryTimeout {
			return nil, fmt.Errorf("retries exhausted: %w", err)
		}
		time.Sleep(guestDialRetryInterval)
	}
}
