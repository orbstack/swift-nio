package icmpfwd

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"

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

func NewIcmpFwd(s *stack.Stack) (*IcmpFwd, error) {
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

	log.Println("Transport: ICMP listener up")
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
	// log.Printf("(client %v) - Transport: ICMP -> %v", netHeader.SourceAddress(), netHeader.DestinationAddress())

	// TODO check if we should forward it
	if packet.NetworkProtocolNumber == ipv4.ProtocolNumber {
		i.conn4.SetTTL(int(netHeader.(header.IPv4).TTL()))
		i.conn4.WriteTo(netHeader.Payload(), nil, &net.UDPAddr{
			IP: net.IP(netHeader.DestinationAddress()),
		})
	} else if packet.NetworkProtocolNumber == ipv6.ProtocolNumber {
		i.conn6.SetHopLimit(int(netHeader.(header.IPv6).HopLimit()))
		i.conn6.WriteTo(netHeader.Payload(), nil, &net.UDPAddr{
			IP: net.IP(netHeader.DestinationAddress()),
		})
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
		buf := fullBuf[:n]

		// msg, err := goicmp.ParseMessage(1, buf[20:n-20])
		// if err != nil {
		// 	log.Println("error parsing icmp message", err)
		// 	return err
		// }

		// switch msg.Type {
		// case goipv4.ICMPTypeEchoReply:
		// 	fmt.Println("got echo reply from", addr.String())
		// case goipv4.ICMPTypeTimeExceeded:
		// 	fmt.Println("got time exceeded from", addr.String(), msg.Body)
		// 	body := msg.Body.(*icmp.TimeExceeded)
		// 	fmt.Println("  data", addr.String(), body.Data)
		// default:
		// 	fmt.Println("got", msg.Type, "from", addr.String())
		// 	// will fail (panic: PullUp failed)
		// 	continue
		// }

		if i.lastSourceAddr4 == "" {
			fmt.Println("no i.lastSourceAddr4")
			continue
		}

		replyHdr := header.IPv4(buf)
		replyHdr.SetDestinationAddress(i.lastSourceAddr4)
		replyHdr.SetTotalLength(uint16(n))
		replyHdr.SetChecksum(0)
		replyHdr.SetChecksum(^replyHdr.CalculateChecksum())
		// decpkt := gopacket.NewPacket(buf, layers.LayerTypeIPv4, gopacket.Default)
		// fmt.Println("reply", decpkt.String())

		r, errT := i.stack.FindRoute(1, replyHdr.SourceAddress(), replyHdr.DestinationAddress(), gvipv4.ProtocolNumber, false /* multicastLoop */)
		if errT != nil {
			log.Printf("FindRoute: %v", errT)
			return errors.New(errT.String())
		}

		replyBuf := bufferv2.MakeWithData(buf)
		replyPkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			ReserveHeaderBytes: int(r.MaxHeaderLength()),
			Payload:            replyBuf,
		})
		defer replyPkt.DecRef()

		netEp, errT := i.stack.GetNetworkEndpoint(1, ipv4.ProtocolNumber)
		if errT != nil {
			log.Printf("SendPacket: %v", errT)
			return errors.New(errT.String())
		}
		if err := netEp.WriteHeaderIncludedPacket(r, replyPkt); err != nil {
			log.Printf("SendPacket: %v", err)
			return errors.New(err.String())
		}
	}
}
