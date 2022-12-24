package dnssrv

import (
	"context"
	"fmt"
	"net"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
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
	for _, q := range r.Question {
		// TODO handle errors - by forwarding?
		switch q.Qtype {
		case dns.TypeA:
			ips, err := h.sysResolver.LookupIP(context.TODO(), "ip4", q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupIP() =", err)
				continue
			}

			for _, ip := range ips {
				m.Answer = append(m.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					A: ip,
				})
			}
		case dns.TypeAAAA:
			ips, err := h.sysResolver.LookupIP(context.TODO(), "ip6", q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupIP() =", err)
				continue
			}

			for _, ip := range ips {
				m.Answer = append(m.Answer, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					AAAA: ip,
				})
			}
		case dns.TypeCNAME:
			cname, err := h.sysResolver.LookupCNAME(context.TODO(), q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupCNAME() =", err)
				continue
			}

			m.Answer = append(m.Answer, &dns.CNAME{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeCNAME,
					Class:  dns.ClassINET,
					Ttl:    0,
				},
				Target: cname,
			})
		// TODO doesn't use sys resolver
		case dns.TypeMX:
			mxs, err := h.sysResolver.LookupMX(context.TODO(), q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupMX() =", err)
				continue
			}

			for _, mx := range mxs {
				m.Answer = append(m.Answer, &dns.MX{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeMX,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					Preference: mx.Pref,
					Mx:         mx.Host,
				})
			}
		// TODO doesn't use sys resolver
		case dns.TypeNS:
			nss, err := h.sysResolver.LookupNS(context.TODO(), q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupNS() =", err)
				continue
			}

			for _, ns := range nss {
				m.Answer = append(m.Answer, &dns.NS{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeNS,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					Ns: ns.Host,
				})
			}
		case dns.TypePTR:
			// TODO wrong addr
			ptr, err := h.sysResolver.LookupAddr(context.TODO(), q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupAddr() =", err)
				continue
			}

			for _, p := range ptr {
				m.Answer = append(m.Answer, &dns.PTR{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypePTR,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					Ptr: p,
				})
			}
		// TODO doesn't use sys resolver
		case dns.TypeTXT:
			txts, err := h.sysResolver.LookupTXT(context.TODO(), q.Name)
			if err != nil {
				fmt.Println("h.sysResolver.LookupTXT() =", err)
				continue
			}

			for _, txt := range txts {
				m.Answer = append(m.Answer, &dns.TXT{
					Hdr: dns.RR_Header{
						Name:   q.Name,
						Rrtype: dns.TypeTXT,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					Txt: []string{txt},
				})
			}
		// TODO: SRV (doesn't use sys resolver...)
		default:
			continue
		}

		// TODO: if we can't answer it, forward it
	}

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
