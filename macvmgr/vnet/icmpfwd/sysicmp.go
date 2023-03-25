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
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	gvicmp "gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
)

const (
	maxIcmp6PktSize = 1280
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
	gatewayAddr4    tcpip.Address
	gatewayAddr6    tcpip.Address
}

func newFlowLabel(s *stack.Stack) uint32 {
	return uint32(s.Rand().Int31n(0xfffff))
}

func extractPacketPayload(pkt stack.PacketBufferPtr) []byte {
	// ToView().AsSlice() includes Ethernet header
	// HeaderSize() includes ALL headers: Ethernet + IP + ICMP
	// We want to strip Ethernet+IP and leave ICMP (or whatever the transport is)
	icmpHdrSize := len(pkt.TransportHeader().Slice())
	return pkt.ToView().AsSlice()[pkt.HeaderSize()-icmpHdrSize:]
}

// don't set STRIPHDR - we want the IP header
func newIcmpPacketConn4() (*goipv4.PacketConn, error) {
	s, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(s)

	// all zero = any
	sa := &unix.SockaddrInet4{}
	if err := unix.Bind(s, sa); err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(s), "icmp4")
	c, err := net.FilePacketConn(f)
	if err != nil {
		return nil, err
	}
	return goipv4.NewPacketConn(c), nil
}

// don't set STRIPHDR - we want the IP header
func newIcmpPacketConn6() (*goipv6.PacketConn, error) {
	s, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_ICMPV6)
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(s)

	// all zero = any
	sa := &unix.SockaddrInet6{}
	if err := unix.Bind(s, sa); err != nil {
		return nil, err
	}

	f := os.NewFile(uintptr(s), "icmp6")
	c, err := net.FilePacketConn(f)
	if err != nil {
		return nil, err
	}
	return goipv6.NewPacketConn(c), nil
}

// reply handling: we cheat and don't implement conntrack
// we just send all incoming ICMP packets that macOS sends us to Linux, and set the source ip to all the same
// Linux NAT will discard any replies it didn't send, so scon machines will never see them
func NewIcmpFwd(s *stack.Stack, nicId tcpip.NICID, initialAddr4, initialAddr6, gatewayAddr4, gatewayAddr6 tcpip.Address) (*IcmpFwd, error) {
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
		gatewayAddr4:    gatewayAddr4,
		gatewayAddr6:    gatewayAddr6,
	}, nil
}

func (i *IcmpFwd) ProxyRequests() {
	i.stack.SetTransportProtocolHandler(gvicmp.ProtocolNumber4, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		i.lastSourceAddr4 = pkt.Network().SourceAddress()
		return i.sendPacket(pkt)
	})

	i.stack.SetTransportProtocolHandler(gvicmp.ProtocolNumber6, func(id stack.TransportEndpointID, pkt stack.PacketBufferPtr) bool {
		i.lastSourceAddr6 = pkt.Network().SourceAddress()
		return i.sendPacket(pkt)
	})
}

// handleICMPMessage parses ICMP packets and proxies them if possible.
func (i *IcmpFwd) sendPacket(pkt stack.PacketBufferPtr) bool {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			logrus.WithError(err).Error("failed to send outgoing ICMP packet")
		}
	}()

	ipHdr := pkt.Network()
	icmpMsg := extractPacketPayload(pkt)
	dstAddr := &net.UDPAddr{
		IP: net.IP(ipHdr.DestinationAddress()),
	}

	// Only forward ICMP Echo Request.
	// For IPv4, macOS also allows Timestamp and Address Mask Request
	// For IPv6, macOS also allows Node Information Query
	// But no one uses them, so don't bother
	if pkt.NetworkProtocolNumber == ipv4.ProtocolNumber {
		if len(icmpMsg) < header.ICMPv4MinimumSize {
			logrus.Trace("discarding ICMPv4 packet: too short")
			return false
		}

		// Check type
		icmpHdr := header.ICMPv4(icmpMsg)
		if icmpHdr.Type() != header.ICMPv4Echo {
			logrus.Trace("discarding ICMPv4 packet: not echo")
			return false
		}

		// TTL
		ip4Hdr := ipHdr.(header.IPv4)
		tos, _ := ip4Hdr.TOS()
		i.conn4.SetTTL(int(ip4Hdr.TTL()))
		i.conn4.SetTOS(int(tos))

		_, err := i.conn4.WriteTo(icmpMsg, nil, dstAddr)
		if err != nil {
			logrus.WithError(err).Error("icmp4 write failed")
			if errors.Is(err, unix.ENETUNREACH) {
				err = i.InjectDestUnreachable4(pkt, header.ICMPv4NetUnreachable)
				if err != nil {
					logrus.WithError(err).Error("icmp4 inject unreachable failed")
				}
			} else if errors.Is(err, unix.EHOSTUNREACH) || errors.Is(err, unix.EHOSTDOWN) {
				err = i.InjectDestUnreachable4(pkt, header.ICMPv4HostUnreachable)
				if err != nil {
					logrus.WithError(err).Error("icmp4 inject unreachable failed")
				}
			}
			return false
		}
		return true
	} else if pkt.NetworkProtocolNumber == ipv6.ProtocolNumber {
		if len(icmpMsg) < header.ICMPv6MinimumSize {
			logrus.Trace("discarding ICMPv6 packet: too short")
			return false
		}

		// Check type
		icmpHdr := header.ICMPv6(icmpMsg)
		if icmpHdr.Type() != header.ICMPv6EchoRequest {
			logrus.Trace("discarding ICMPv6 packet: not echo")
			return false
		}

		// TTL
		ip6Hdr := ipHdr.(header.IPv6)
		trafficClass, _ := ip6Hdr.TOS()
		i.conn6.SetHopLimit(int(ip6Hdr.HopLimit()))
		i.conn6.SetTrafficClass(int(trafficClass))

		_, err := i.conn6.WriteTo(icmpMsg, nil, dstAddr)
		if err != nil {
			logrus.WithError(err).Error("icmp6 write failed")
			if errors.Is(err, unix.ENETUNREACH) {
				err = i.InjectDestUnreachable6(pkt, header.ICMPv6NetworkUnreachable)
				if err != nil {
					logrus.WithError(err).Error("icmp6 inject unreachable failed")
				}
			} else if errors.Is(err, unix.EHOSTUNREACH) || errors.Is(err, unix.EHOSTDOWN) {
				err = i.InjectDestUnreachable6(pkt, header.ICMPv6AddressUnreachable)
				if err != nil {
					logrus.WithError(err).Error("icmp6 inject unreachable failed")
				}
			}
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
	case header.ICMPv4DstUnreachable, header.ICMPv4TimeExceeded:
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
	ipHdr.SetTOS(uint8(cm.TrafficClass), newFlowLabel(i.stack))
	ipHdr.SetDestinationAddress(i.lastSourceAddr6)
	ipHdr.SetSourceAddress(tcpip.Address(addr.(*net.UDPAddr).IP.To16()))
	ipHdr.SetNextHeader(uint8(gvicmp.ProtocolNumber6))

	icmpHdr := header.ICMPv6(ipHdr.Payload())
	switch icmpHdr.Type() {
	case header.ICMPv6EchoReply:
		// do nothing special
	// Types with nested payloads: need to fix nested packet
	case header.ICMPv6DstUnreachable, header.ICMPv6TimeExceeded:
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

// for now, only ipv6 because we really need it in case there's no IPv6 on host
func (i *IcmpFwd) InjectDestUnreachable6(pkt stack.PacketBufferPtr, code header.ICMPv6Code) error {
	// Make a new IP header
	// TODO do this better
	payload := append(pkt.NetworkHeader().View().ToSlice(), extractPacketPayload(pkt)...)
	totalLen := header.IPv6MinimumSize + header.ICMPv6MinimumSize + len(payload)
	if totalLen > maxIcmp6PktSize {
		totalLen = maxIcmp6PktSize
	}
	payloadLen := totalLen - header.IPv6MinimumSize - header.ICMPv6MinimumSize
	if len(payload) > payloadLen {
		payload = payload[:payloadLen]
	}

	msg := make([]byte, totalLen)
	ipHdr := header.IPv6(msg)
	ipHdr.SetPayloadLength(uint16(len(msg) - header.IPv6MinimumSize))
	ipHdr.SetHopLimit(64)
	// TODO traffic class
	ipHdr.SetTOS(0, newFlowLabel(i.stack))
	ipHdr.SetDestinationAddress(i.lastSourceAddr6)
	ipHdr.SetSourceAddress(i.gatewayAddr6)
	ipHdr.SetNextHeader(uint8(gvicmp.ProtocolNumber6))

	icmpHdr := header.ICMPv6(ipHdr.Payload())
	icmpHdr.SetType(header.ICMPv6DstUnreachable)
	icmpHdr.SetCode(code)
	icmpHdr.SetChecksum(header.ICMPv6Checksum(header.ICMPv6ChecksumParams{
		Header:      icmpHdr,
		Src:         i.gatewayAddr6,
		Dst:         i.lastSourceAddr6,
		PayloadCsum: checksum.Checksum(payload, 0),
		PayloadLen:  pkt.Size(),
	}))

	// Copy payload
	copy(icmpHdr.Payload(), payload)

	return i.sendReply(ipv6.ProtocolNumber, i.gatewayAddr6, i.lastSourceAddr6, msg)
}

func (i *IcmpFwd) InjectDestUnreachable4(pkt stack.PacketBufferPtr, code header.ICMPv4Code) error {
	// Make a new IP header
	// TODO do this better
	payload := append(pkt.NetworkHeader().View().ToSlice(), extractPacketPayload(pkt)...)
	// only take IP header + first 8 bytes of payload
	// to be lazy, we violate this and use IPv4 max heade size
	if len(payload) > header.IPv4MaximumHeaderSize+8 {
		payload = payload[:header.IPv4MaximumHeaderSize+8]
	}
	totalLen := header.IPv4MinimumSize + header.ICMPv4MinimumSize + len(payload)

	msg := make([]byte, totalLen)
	ipHdr := header.IPv4(msg)
	ipHdr.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(msg)),
		TTL:         64,
		Protocol:    uint8(gvicmp.ProtocolNumber4),
		SrcAddr:     i.gatewayAddr4,
		DstAddr:     i.lastSourceAddr4,
	})

	icmpHdr := header.ICMPv4(ipHdr.Payload())
	icmpHdr.SetType(header.ICMPv4DstUnreachable)
	icmpHdr.SetCode(code)
	icmpHdr.SetChecksum(0)
	icmpHdr.SetChecksum(header.ICMPv4Checksum(icmpHdr, 0))

	// Copy payload
	copy(icmpHdr.Payload(), payload)

	ipHdr.SetChecksum(0)
	ipHdr.SetChecksum(^ipHdr.CalculateChecksum())

	return i.sendReply(ipv4.ProtocolNumber, i.gatewayAddr4, i.lastSourceAddr4, msg)
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
