package icmpfwd

import (
	"errors"
	"fmt"
	"log"
	"net"

	"golang.org/x/net/icmp"
	goicmp "golang.org/x/net/icmp"
	goipv4 "golang.org/x/net/ipv4"
	"gvisor.dev/gvisor/pkg/bufferv2"
	"gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/header/parse"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	gvipv4 "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	gvipv6 "gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	gvicmp "gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
)

type IcmpFwd struct {
	stack      *stack.Stack
	conn4      *goicmp.PacketConn
	conn6      *goicmp.PacketConn
	lastOutPkt *stack.PacketBufferPtr
}

func NewIcmpFwd(s *stack.Stack) (*IcmpFwd, error) {
	conn4, err := goicmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	conn6, err := goicmp.ListenPacket("udp6", "::")
	if err != nil {
		return nil, err
	}

	return &IcmpFwd{s, conn4, conn6, nil}, nil
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
			i.sendOut(clonedPacket)
			//TODO
			//clonedPacket.DecRef()
		}()
	}
}

// handleICMPMessage parses ICMP packets and proxies them if possible.
func (i *IcmpFwd) sendOut(packet stack.PacketBufferPtr) {
	// Parse ICMP packet type.
	netHeader := packet.Network()
	log.Printf("(client %v) - Transport: ICMP -> %v", netHeader.SourceAddress(), netHeader.DestinationAddress())

	// TODO check if we should do this one
	conn := i.conn4
	if packet.NetworkProtocolNumber == ipv6.ProtocolNumber {
		conn = i.conn6
	}
	// transHeader := header.ICMPv6(netHeader.Payload())
	// switch transHeader.Type() {
	// case header.ICMPv6EchoRequest

	i.lastOutPkt = &packet
	conn.WriteTo(netHeader.Payload(), &net.UDPAddr{
		IP: net.IP(netHeader.DestinationAddress()),
	})
}

func (i *IcmpFwd) MonitorReplies(ep stack.LinkEndpoint) error {
	buf := make([]byte, 65535)
	for {
		n, addr, err := i.conn4.ReadFrom(buf)
		if err != nil {
			return err
		}

		msg, err := goicmp.ParseMessage(1, buf[:n])
		if err != nil {
			return err
		}

		switch msg.Type {
		case goipv4.ICMPTypeEchoReply:
			fmt.Println("got echo reply from", addr.String())
		case goipv4.ICMPTypeTimeExceeded:
			fmt.Println("got time exceeded from", addr.String(), msg.Body)
			body := msg.Body.(*icmp.TimeExceeded)
			fmt.Println("  data", addr.String(), body.Data)
		default:
			fmt.Println("got", msg.Type, "from", addr.String())
			// will fail (panic: PullUp failed)
			continue
		}

		// make ip header
		// srcAddr := tcpip.Address(addr.(*net.UDPAddr).IP)
		// if i.lastOutPkt == nil {
		// 	fmt.Println("no i.lastOutPkt")
		// 	continue
		// }
		ipHdr := header.IPv4(i.lastOutPkt.NetworkHeader().Slice())
		localAddr := ipHdr.DestinationAddress()
		r, errT := i.stack.FindRoute(1, localAddr, ipHdr.SourceAddress(), gvipv4.ProtocolNumber, false /* multicastLoop */)
		if errT != nil {
			return errors.New(errT.String())
		}
		newOptions := make([]byte, 0)
		replyData := bufferv2.NewViewWithData(buf[:n])
		replyHeaderLength := uint8(header.IPv4MinimumSize + len(newOptions))
		replyIPHdrView := bufferv2.NewView(int(replyHeaderLength))
		replyIPHdrView.Write(ipHdr[:header.IPv4MinimumSize])
		replyIPHdrView.Write(newOptions)
		replyIPHdr := header.IPv4(replyIPHdrView.AsSlice())
		replyIPHdr.SetHeaderLength(replyHeaderLength)
		replyIPHdr.SetSourceAddress(r.LocalAddress())
		replyIPHdr.SetDestinationAddress(r.RemoteAddress())
		replyIPHdr.SetTTL(64)
		replyIPHdr.SetTotalLength(uint16(len(replyIPHdr) + len(replyData.AsSlice())))
		replyIPHdr.SetChecksum(0)
		replyIPHdr.SetChecksum(^replyIPHdr.CalculateChecksum())

		replyICMPHdr := header.ICMPv4(replyData.AsSlice())
		replyICMPHdr.SetType(header.ICMPv4EchoReply)
		replyICMPHdr.SetChecksum(0)
		replyICMPHdr.SetChecksum(^checksum.Checksum(replyData.AsSlice(), 0))

		replyBuf := bufferv2.MakeWithView(replyIPHdrView)
		replyBuf.Append(replyData.Clone())
		replyPkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			ReserveHeaderBytes: int(r.MaxHeaderLength()),
			Payload:            replyBuf,
		})
		defer replyPkt.DecRef()

		parse.IPv4(replyPkt)
		parse.ICMPv4(replyPkt)

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
