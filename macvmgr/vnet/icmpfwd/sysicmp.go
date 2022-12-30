package icmpfwd

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/udpfwd"
	"github.com/sirupsen/logrus"
	goipv4 "golang.org/x/net/ipv4"
	goipv6 "golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/bufferv2"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	gvicmp "gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
)

type IcmpFwd struct {
	stack *stack.Stack
	nicId tcpip.NICID
	conn4 *goipv4.PacketConn
	conn6 *goipv6.PacketConn

	// to send reply packets
	// TODO proper connection tracking
	lastSourceAddr4 tcpip.Address
	lastSourceAddr6 tcpip.Address
}

// don't set STRIPHDR - we want the IP header
func newIcmpPacketConn4() (*goipv4.PacketConn, error) {
	s, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}

	// all zero = any
	sa := &unix.SockaddrInet4{}
	if err := unix.Bind(s, sa); err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(s), "icmp4")
	c, err := net.FilePacketConn(f)
	return goipv4.NewPacketConn(c), nil
}

// don't set STRIPHDR - we want the IP header
func newIcmpPacketConn6() (*goipv6.PacketConn, error) {
	s, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_ICMPV6)
	if err != nil {
		return nil, err
	}

	// all zero = any
	sa := &unix.SockaddrInet6{}
	if err := unix.Bind(s, sa); err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(s), "icmp6")
	c, err := net.FilePacketConn(f)
	return goipv6.NewPacketConn(c), nil
}

func NewIcmpFwd(s *stack.Stack, nicId tcpip.NICID, initialAddr4, initialAddr6 tcpip.Address) (*IcmpFwd, error) {
	conn4, err := newIcmpPacketConn4()
	if err != nil {
		return nil, err
	}
	conn6, err := newIcmpPacketConn6()
	if err != nil {
		return nil, err
	}

	return &IcmpFwd{
		stack:           s,
		nicId:           nicId,
		conn4:           conn4,
		conn6:           conn6,
		lastSourceAddr4: initialAddr4,
		lastSourceAddr6: initialAddr6,
	}, nil
}

func (i *IcmpFwd) ProxyRequests() {
	i.stack.SetTransportProtocolHandler(gvicmp.ProtocolNumber4, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		i.lastSourceAddr4 = pkt.Network().SourceAddress()
		return i.sendPkt(pkt)
	})

	i.stack.SetTransportProtocolHandler(gvicmp.ProtocolNumber6, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		i.lastSourceAddr6 = pkt.Network().SourceAddress()
		return i.sendPkt(pkt)
	})
}

// handleICMPMessage parses ICMP packets and proxies them if possible.
func (i *IcmpFwd) sendPkt(pkt stack.PacketBufferPtr) bool {
	ipHdr := pkt.Network()
	icmpMsg := ipHdr.Payload()
	dstAddr := &net.UDPAddr{
		IP: net.IP(ipHdr.DestinationAddress()),
	}

	// Only forward ICMP Echo Request.
	// For IPv4, macOS also allows Timestamp and Address Mask Request
	// For IPv6, macOS also allows Node Information Query
	// But no one uses them, so don't bother
	if pkt.NetworkProtocolNumber == ipv4.ProtocolNumber {
		if len(icmpMsg) < header.ICMPv4MinimumSize {
			return false
		}

		// Check type
		icmpHdr := header.ICMPv4(icmpMsg)
		if icmpHdr.Type() != header.ICMPv4Echo {
			return false
		}

		// TTL
		ip4Hdr := ipHdr.(header.IPv4)
		tos, _ := ip4Hdr.TOS()
		i.conn4.SetTTL(int(ip4Hdr.TTL()))
		i.conn4.SetTOS(int(tos))

		_, err := i.conn4.WriteTo(icmpMsg, nil, dstAddr)
		if err != nil {
			logrus.Error("error writing to icmp4 socket ", err)
			return false
		}
		return true
	} else if pkt.NetworkProtocolNumber == ipv6.ProtocolNumber {
		if len(icmpMsg) < header.ICMPv6MinimumSize {
			return false
		}

		// Check type
		icmpHdr := header.ICMPv6(icmpMsg)
		if icmpHdr.Type() != header.ICMPv6EchoRequest {
			return false
		}

		// TTL
		ip6Hdr := ipHdr.(header.IPv6)
		trafficClass, _ := ip6Hdr.TOS()
		i.conn6.SetHopLimit(int(ip6Hdr.HopLimit()))
		i.conn6.SetTrafficClass(int(trafficClass))

		_, err := i.conn6.WriteTo(icmpMsg, nil, dstAddr)
		if err != nil {
			logrus.Error("error writing to icmp6 socket ", err)
			return false
		}
		return true
	}

	return false
}

func (i *IcmpFwd) MonitorReplies() {
	go i.forwardReplies4()
	go i.forwardReplies6()
}

func (i *IcmpFwd) forwardReplies4() error {
	fullBuf := make([]byte, 65535)
	for {
		n, _, _, err := i.conn4.ReadFrom(fullBuf)
		if err != nil {
			logrus.Error("error reading from icmp4 socket ", err)
			return err
		}
		msg := fullBuf[:n]

		err = i.handleReply4(msg)
		if err != nil {
			logrus.Error("error handling icmp4 reply ", err)
		}
	}
}

func (i *IcmpFwd) handleReply4(msg []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	if len(msg) < header.IPv4MinimumSize+header.ICMPv4MinimumSize {
		return fmt.Errorf("packet too small")
	}

	// Fix the IP header
	ipHdr := header.IPv4(msg)
	// Wrong for UDP, will be fixed below
	ipHdr.SetDestinationAddress(i.lastSourceAddr4)
	ipHdr.SetTotalLength(uint16(len(msg))) // macOS sets 16384

	// Do surgery on packet
	icmpHdr := header.ICMPv4(ipHdr.Payload())
	switch icmpHdr.Type() {
	case header.ICMPv4EchoReply:
		// do nothing special
	// Types with nested payloads: need to fix nested packet
	case header.ICMPv4DstUnreachable:
		fallthrough
	case header.ICMPv4TimeExceeded:
		origMsg := icmpHdr.Payload()
		// Discard too-small packets
		if len(origMsg) < header.IPv4MinimumSize+header.UDPMinimumSize {
			return
		}

		// Fix original IP header
		origIpHdr := header.IPv4(origMsg)
		origIpHdr.SetTotalLength(uint16(len(origMsg))) // macOS sets 16384

		// Fix nested L4 header
		switch origIpHdr.TransportProtocol() {
		// ICMP: fix source IP
		case header.ICMPv4ProtocolNumber:
			if i.lastSourceAddr4 == "" {
				return
			}
			origIpHdr.SetSourceAddress(i.lastSourceAddr4)
		// UDP: fix source IP and port. (IP ident is wrong too)
		case header.UDPProtocolNumber:
			// Find the connection in the UDP conntrack map
			origUdpHdr := header.UDP(origIpHdr[header.IPv4MinimumSize:])
			localSrcAddr := udpfwd.LookupExternalConn(&net.UDPAddr{
				// our external IP, not virtual
				IP:   net.IP(origIpHdr.SourceAddress()),
				Port: int(origUdpHdr.SourcePort()),
			})
			if localSrcAddr == nil {
				return
			}

			// UDP checksum includes IP pseudo-header with addresses. Fix it.
			virtSrcAddr := tcpip.Address(localSrcAddr.IP.To4())
			// If checksum is non-zero, update it
			if origUdpHdr.Checksum() != 0 {
				origUdpHdr.UpdateChecksumPseudoHeaderAddress(origIpHdr.SourceAddress(), virtSrcAddr, true)
				origUdpHdr.SetSourcePortWithChecksumUpdate(uint16(localSrcAddr.Port))
			} else {
				origUdpHdr.SetSourcePort(uint16(localSrcAddr.Port))
			}
			// Then fix original source IP
			origIpHdr.SetSourceAddress(tcpip.Address(localSrcAddr.IP.To4()))
			// Fix reply IP destination
			ipHdr.SetDestinationAddress(tcpip.Address(localSrcAddr.IP.To4()))
		// TCP: not supported
		case header.TCPProtocolNumber:
		default:
			return
		}

		// Fix orig IP checksum (after updating)
		origIpHdr.SetChecksum(0)
		origIpHdr.SetChecksum(^origIpHdr.CalculateChecksum())

		// Fix ICMP checksum
		icmpHdr.SetChecksum(0)
		icmpHdr.SetChecksum(header.ICMPv4Checksum(icmpHdr, 0))
	default:
		return
	}

	// Fix IP checksum
	ipHdr.SetChecksum(0)
	ipHdr.SetChecksum(^ipHdr.CalculateChecksum())

	return i.sendReply(ipv4.ProtocolNumber, ipHdr.SourceAddress(), ipHdr.DestinationAddress(), msg)
}

func (i *IcmpFwd) forwardReplies6() error {
	i.conn6.SetControlMessage(goipv6.FlagTrafficClass, true)
	i.conn6.SetControlMessage(goipv6.FlagHopLimit, true)
	i.conn6.SetControlMessage(goipv6.FlagDst, true)

	fullBuf := make([]byte, 65535)
	for {
		n, cm, addr, err := i.conn6.ReadFrom(fullBuf)
		if err != nil {
			logrus.Error("error reading from icmp6 socket ", err)
			return err
		}
		msg := fullBuf[:n]

		err = i.handleReply6(msg, cm, addr)
		if err != nil {
			logrus.Error("error handling icmp6 reply ", err)
		}
	}
}

func (i *IcmpFwd) handleReply6(msg []byte, cm *goipv6.ControlMessage, addr net.Addr) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	if len(msg) < header.ICMPv6MinimumSize {
		return
	}

	// Make a new IP header
	replyMsg := make([]byte, len(msg)+header.IPv6MinimumSize)
	copy(replyMsg[header.IPv6MinimumSize:], msg)
	ipHdr := header.IPv6(replyMsg)
	ipHdr.SetPayloadLength(uint16(len(msg)))
	ipHdr.SetHopLimit(uint8(cm.HopLimit))
	ipHdr.SetTOS(uint8(cm.TrafficClass), 0) // flow label = 0
	ipHdr.SetDestinationAddress(i.lastSourceAddr6)
	ipHdr.SetSourceAddress(tcpip.Address(addr.(*net.UDPAddr).IP.To16()))
	ipHdr.SetNextHeader(uint8(gvicmp.ProtocolNumber6))

	icmpHdr := header.ICMPv6(ipHdr.Payload())
	switch icmpHdr.Type() {
	case header.ICMPv6EchoReply:
		// do nothing special
	// Types with nested payloads: need to fix nested packet
	case header.ICMPv6DstUnreachable:
		fallthrough
	case header.ICMPv6TimeExceeded:
		origMsg := icmpHdr.Payload()
		// Discard too-small packets
		if len(origMsg) < header.IPv6MinimumSize+header.UDPMinimumSize {
			return
		}

		// Fix original IP header
		origIpHdr := header.IPv6(origMsg)
		origIpHdr.SetPayloadLength(uint16(len(origMsg) - header.IPv6MinimumSize)) // macOS sets 16384

		// Fix nested L4 header
		switch origIpHdr.TransportProtocol() {
		// ICMP: fix source IP
		case header.ICMPv6ProtocolNumber:
			if i.lastSourceAddr6 == "" {
				return
			}
			origIpHdr.SetSourceAddress(i.lastSourceAddr6)
		// UDP: fix source IP and port. (IP ident is wrong too)
		case header.UDPProtocolNumber:
			// Find the connection in the UDP conntrack map
			origUdpHdr := header.UDP(origIpHdr[header.IPv6MinimumSize:])
			localSrcAddr := udpfwd.LookupExternalConn(&net.UDPAddr{
				// our external IP, not virtual
				IP:   net.IP(origIpHdr.SourceAddress()),
				Port: int(origUdpHdr.SourcePort()),
			})
			if localSrcAddr == nil {
				return
			}

			// UDP checksum includes IP pseudo-header with addresses. Fix it.
			virtSrcAddr := tcpip.Address(localSrcAddr.IP.To16())
			// If checksum is non-zero, update it
			if origUdpHdr.Checksum() != 0 {
				origUdpHdr.UpdateChecksumPseudoHeaderAddress(origIpHdr.SourceAddress(), virtSrcAddr, true)
				origUdpHdr.SetSourcePortWithChecksumUpdate(uint16(localSrcAddr.Port))
			} else {
				origUdpHdr.SetSourcePort(uint16(localSrcAddr.Port))
			}
			// Then fix original source IP
			origIpHdr.SetSourceAddress(tcpip.Address(localSrcAddr.IP.To16()))
			// Fix reply IP destination
			ipHdr.SetDestinationAddress(tcpip.Address(localSrcAddr.IP.To16()))
		// TCP: not supported
		case header.TCPProtocolNumber:
		default:
			return
		}
	default:
		// Drop Neighbor S/A, RA/RS, etc.
		return
	}

	// Fix ICMP checksum for NAT
	icmpHdr.UpdateChecksumPseudoHeaderAddress(tcpip.Address(cm.Dst), ipHdr.DestinationAddress())

	return i.sendReply(ipv6.ProtocolNumber, ipHdr.SourceAddress(), ipHdr.DestinationAddress(), replyMsg)
}

func (i *IcmpFwd) sendReply(netProto tcpip.NetworkProtocolNumber, srcAddr, dstAddr tcpip.Address, msg []byte) error {
	r, err := i.stack.FindRoute(i.nicId, srcAddr, dstAddr, netProto, false)
	if err != nil {
		return errors.New(err.String())
	}
	defer r.Release()

	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
		ReserveHeaderBytes: int(r.MaxHeaderLength()),
		Payload:            bufferv2.MakeWithData(msg),
	})
	defer pkt.DecRef()

	netEp, err := i.stack.GetNetworkEndpoint(i.nicId, netProto)
	if err != nil {
		return errors.New(err.String())
	}

	err = netEp.WriteHeaderIncludedPacket(r, pkt)
	if err != nil {
		return errors.New(err.String())
	}
	return nil
}
