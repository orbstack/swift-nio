package dnssrv

import (
	"fmt"
	"net"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmm/vnet/services/dns/dnssd"
	"github.com/miekg/dns"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	DNSPort = 53
)

// TODO aliases
type dnsHandler struct {
	sysResolver net.Resolver
}

func (h *dnsHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true

	// TODO check other fields
	// TTLs and classes wrong
	for _, q := range r.Question {
		// dns-sd only support sthis
		if q.Qclass != dns.ClassINET {
			continue
		}

		fmt.Println("query dnssd")

		answers, err := dnssd.Query(q.Name, q.Qtype)
		if err != nil {
			fmt.Println("dnssd.Query() =", err)
			continue
		}
		for _, a := range answers {
			fmt.Println("answer =", a)
			hdr := dns.RR_Header{
				Name:     a.Name,
				Rrtype:   a.Type,
				Class:    a.Class,
				Ttl:      a.TTL,
				Rdlength: uint16(len(a.Data)),
			}
			rr, _, err := dns.UnpackRRWithHeader(hdr, a.Data, 0)
			if err != nil {
				fmt.Println("dns.UnpackRRWithHeader() =", err)
				continue
			}
			fmt.Println("rr", rr)
			m.Answer = append(m.Answer, rr)
		}
	}

	fmt.Println("=>m", m)

	if err := w.WriteMsg(m); err != nil {
		fmt.Println("w.WriteMsg() =", err)
	}
}

func ListenDNS(stack *stack.Stack, address tcpip.Address) error {
	udpConn, err := gonet.DialUDP(stack, &tcpip.FullAddress{
		Addr: address,
		Port: DNSPort,
	}, nil, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	tcpListener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: DNSPort,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	handler := &dnsHandler{
		sysResolver: net.Resolver{
			PreferGo:     false,
			StrictErrors: false,
		},
	}
	mux := dns.NewServeMux()
	mux.Handle(".", handler)

	// UDP
	go func() {
		server := &dns.Server{PacketConn: udpConn, Handler: mux}
		err := server.ActivateAndServe()
		if err != nil {
			fmt.Println("dns.Server.ActivateAndServe() =", err)
		}
	}()

	// TCP
	go func() {
		server := &dns.Server{Listener: tcpListener, Handler: mux}
		err := server.ActivateAndServe()
		if err != nil {
			fmt.Println("dns.Server.ActivateAndServe() =", err)
		}
	}()

	return nil
}
