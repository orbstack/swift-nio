package icmpfwd

import (
	"log"
	"net"
	"os"

	"github.com/kdrag0n/macvirt/macvmm/network/udpfwd"
	goipv4 "golang.org/x/net/ipv4"
	goipv6 "golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/bufferv2"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	gvipv4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	gvipv6 "gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	gvicmp "gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
)

type IcmpFwd struct {
	stack *stack.Stack
	nicId tcpip.NICID
	conn4 *goipv4.PacketConn
	conn6 *goipv6.PacketConn
	// to send reply packets
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

	f := os.NewFile(uintptr(s), "icmp")
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

	f := os.NewFile(uintptr(s), "icmp")
	c, err := net.FilePacketConn(f)
	return goipv6.NewPacketConn(c), nil
}

func NewIcmpFwd(s *stack.Stack, nicId tcpip.NICID) (*IcmpFwd, error) {
	conn4, err := newIcmpPacketConn4()
	if err != nil {
		return nil, err
	}
	conn6, err := newIcmpPacketConn6()
	if err != nil {
		return nil, err
	}

	return &IcmpFwd{
		stack: s,
		nicId: nicId,
		conn4: conn4,
		conn6: conn6,
	}, nil
}

func (i *IcmpFwd) ProxyRequests() {
	// create iptables rule that drops icmp, but clones packet and sends it to this handler.
	headerFilter4 := stack.IPHeaderFilter{
		Protocol:      gvicmp.ProtocolNumber4,
		CheckProtocol: true,
	}

	headerFilter6 := stack.IPHeaderFilter{
		Protocol:      gvicmp.ProtocolNumber6,
		CheckProtocol: true,
	}

	match := preroutingMatch{
		pktChan: make(chan stack.PacketBufferPtr),
	}

	rule4 := stack.Rule{
		Filter:   headerFilter4,
		Matchers: []stack.Matcher{match},
		Target: &stack.DropTarget{
			NetworkProtocol: gvipv4.ProtocolNumber,
		},
	}

	rule6 := stack.Rule{
		Filter:   headerFilter6,
		Matchers: []stack.Matcher{match},
		Target: &stack.DropTarget{
			NetworkProtocol: gvipv6.ProtocolNumber,
		},
	}

	tid := stack.NATID
	PushRule(i.stack, rule4, tid, false)
	PushRule(i.stack, rule6, tid, true)

	for {
		clonedPacket := <-match.pktChan
		go func() {
			defer clonedPacket.DecRef()
			if clonedPacket.NetworkProtocolNumber == ipv4.ProtocolNumber {
				i.lastSourceAddr4 = clonedPacket.Network().SourceAddress()
			} else if clonedPacket.NetworkProtocolNumber == ipv6.ProtocolNumber {
				i.lastSourceAddr6 = clonedPacket.Network().SourceAddress()
			}
			i.sendOut(clonedPacket)
		}()
	}
}

// handleICMPMessage parses ICMP packets and proxies them if possible.
func (i *IcmpFwd) sendOut(packet stack.PacketBufferPtr) {
	// Parse ICMP packet type.
	netHeader := packet.Network()
	//log.Printf("(client %v) - Transport: ICMP -> %v", netHeader.SourceAddress(), netHeader.DestinationAddress())

	// TODO check if we should forward it
	if packet.NetworkProtocolNumber == ipv4.ProtocolNumber {
		i.conn4.SetTTL(int(netHeader.(header.IPv4).TTL()))
		_, err := i.conn4.WriteTo(netHeader.Payload(), nil, &net.UDPAddr{
			IP: net.IP(netHeader.DestinationAddress()),
		})
		// TODO dont print
		if err != nil {
			log.Println("error writing to icmp4 socket", err)
		}
	} else if packet.NetworkProtocolNumber == ipv6.ProtocolNumber {
		i.conn6.SetHopLimit(int(netHeader.(header.IPv6).HopLimit()))
		_, err := i.conn6.WriteTo(netHeader.Payload(), nil, &net.UDPAddr{
			IP: net.IP(netHeader.DestinationAddress()),
		})
		if err != nil {
			log.Println("error writing to icmp6 socket", err)
		}
	}
}

func (i *IcmpFwd) MonitorReplies(ep stack.LinkEndpoint) error {
	fullBuf := make([]byte, 65535)
	for {
		n, _, _, err := i.conn4.ReadFrom(fullBuf)
		if err != nil {
			log.Println("error reading from icmp socket", err)
			return err
		}
		msg := fullBuf[:n]

		// Fix the IP header
		ipHdr := header.IPv4(msg)
		// Wrong for UDP, will be fixed below
		ipHdr.SetDestinationAddress(i.lastSourceAddr4)
		ipHdr.SetTotalLength(uint16(n)) // macOS sets 16384

		icmpHdr := header.ICMPv4(msg[ipHdr.HeaderLength():])
		if icmpHdr.Type() == header.ICMPv4TimeExceeded {
			origMsg := icmpHdr.Payload()
			// Discard too-small packets
			if len(origMsg) < header.IPv4MinimumSize+header.UDPMinimumSize {
				continue
			}

			// Fix original IP header
			origIpHdr := header.IPv4(origMsg)
			origIpHdr.SetTotalLength(uint16(len(origMsg))) // macOS sets 16384

			// Fix nested L4 header
			switch origIpHdr.TransportProtocol() {
			// ICMP: fix source IP
			case header.ICMPv4ProtocolNumber:
				if i.lastSourceAddr4 == "" {
					continue
				}
				origIpHdr.SetSourceAddress(i.lastSourceAddr4)
			// UDP: fix source IP and port. (IP ident is wrong too)
			case header.UDPProtocolNumber:
				// Find the connection in the UDP conntrack map
				origUdpHdr := header.UDP(origMsg[origIpHdr.HeaderLength():])
				localSrcAddr := udpfwd.LookupExternalConn(&net.UDPAddr{
					// our external IP, not virtual
					IP:   net.IP(origIpHdr.SourceAddress()),
					Port: int(origUdpHdr.SourcePort()),
				})
				if localSrcAddr == nil {
					continue
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
				continue
			}

			// Fix orig IP checksum (after updating)
			origIpHdr.SetChecksum(0)
			origIpHdr.SetChecksum(^origIpHdr.CalculateChecksum())

			// Fix ICMP checksum
			icmpHdr.SetChecksum(0)
			icmpHdr.SetChecksum(header.ICMPv4Checksum(icmpHdr, 0))
		}

		// Fix IP checksum
		ipHdr.SetChecksum(0)
		ipHdr.SetChecksum(^ipHdr.CalculateChecksum())

		// decpkt := gopacket.NewPacket(msg, layers.LayerTypeIPv4, gopacket.Default)
		// fmt.Println("reply", decpkt.String())

		r, errT := i.stack.FindRoute(i.nicId, ipHdr.SourceAddress(), ipHdr.DestinationAddress(), gvipv4.ProtocolNumber, false)
		if errT != nil {
			log.Printf("FindRoute: %v", errT)
			continue
		}
		defer r.Release()

		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			ReserveHeaderBytes: int(r.MaxHeaderLength()),
			Payload:            bufferv2.MakeWithData(msg),
		})
		defer pkt.DecRef()

		netEp, errT := i.stack.GetNetworkEndpoint(i.nicId, ipv4.ProtocolNumber)
		if errT != nil {
			log.Printf("SendPacket: %v", errT)
			continue
		}
		if err := netEp.WriteHeaderIncludedPacket(r, pkt); err != nil {
			log.Printf("SendPacket: %v", err)
			continue
		}
	}
}
