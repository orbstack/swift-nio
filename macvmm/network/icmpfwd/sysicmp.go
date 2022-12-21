package icmpfwd

import (
	"errors"
	"fmt"
	"log"
	"net"

	"golang.org/x/net/icmp"
	goicmp "golang.org/x/net/icmp"
	goipv4 "golang.org/x/net/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
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

func (i *IcmpFwd) MonitorReplies() error {
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
		srcAddr := tcpip.Address(addr.(*net.UDPAddr).IP)
		if i.lastOutPkt == nil {
			fmt.Println("no i.lastOutPkt")
			continue
		}
		netHeader := i.lastOutPkt.Network().(header.IPv4)

		// write it
		srcFullAddr := &tcpip.FullAddress{
			NIC:  1,
			Addr: netHeader.DestinationAddress(),
		}
		netHeader.SetDestinationAddress(netHeader.SourceAddress())
		netHeader.SetSourceAddress(srcAddr)
		netHeader.SetChecksum(0)
		netHeader.SetChecksum(^netHeader.CalculateChecksum())
		netHeader.SetTotalLength(uint16(len(netHeader) + n))
		if err := SendPacket(i.stack, append(netHeader, buf[:n]...), srcFullAddr, gvipv4.ProtocolNumber); err != nil {
			log.Printf("SendPacket: %v", err)
			return errors.New(err.String())
		}
	}
}
