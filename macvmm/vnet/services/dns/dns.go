package dnssrv

import (
	"fmt"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmm/vnet/netutil"
	"github.com/kdrag0n/macvirt/macvmm/vnet/services/dns/dnssd"
	"github.com/miekg/dns"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	DNSPort = 53
)

type StaticHost struct {
	IP4 string
	IP6 string
}

type dnsHandler struct{}

func sendReply(w dns.ResponseWriter, req *dns.Msg, msg *dns.Msg, isUdp bool) {
	// EDNS and truncation
	ednsOpt := req.IsEdns0()
	if ednsOpt != nil {
		msg.SetEdns0(ednsOpt.UDPSize(), false)
		if isUdp {
			msg.Truncate(int(ednsOpt.UDPSize()))
		} else {
			msg.Truncate(dns.MaxMsgSize)
		}
	} else {
		if isUdp {
			msg.Truncate(dns.MinMsgSize)
		} else {
			msg.Truncate(dns.MaxMsgSize)
		}
	}

	if err := w.WriteMsg(msg); err != nil {
		fmt.Println("w.WriteMsg() =", err)
	}
}

func (h *dnsHandler) handleDnsReq(w dns.ResponseWriter, req *dns.Msg, isUdp bool) {
	msg := new(dns.Msg)
	msg.SetReply(req)
	msg.RecursionAvailable = true

	for _, q := range req.Question {
		// dns-sd only support this
		if q.Qclass != dns.ClassINET {
			continue
		}

		fmt.Println("query dnssd")

		answers, err := dnssd.QueryRecursive(q.Name, q.Qtype)
		if err != nil {
			fmt.Println("dnssd.QueryRecursive() =", err)

			// No network? macOS returns NXDOMAIN but let's return timeout
			if (err == dnssd.ErrNoSuchRecord || err == dnssd.ErrNoSuchName) && netutil.GetDefaultAddress4() == nil {
				return
			}

			// Default error handling
			switch err {
			// simulate timeout
			case dnssd.ErrTimeout:
				return
			case dnssd.ErrServiceNotRunning:
				return
			case dnssd.ErrDefunctConnection:
				return
			case dnssd.ErrBadInterfaceIndex:
				return
			case dnssd.ErrFirewall:
				return
			// return an error
			default:
				msg.Rcode = mapErrorcode(err)
			}
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
			msg.Answer = append(msg.Answer, rr)
		}
	}

	fmt.Println("=>m", msg)
	sendReply(w, req, msg, isUdp)
}

func ListenDNS(stack *stack.Stack, address tcpip.Address, staticHosts map[string]StaticHost) error {
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

	staticRrs := map[string][]dns.RR{}
	for host, staticHost := range staticHosts {
		if staticHost.IP4 != "" {
			rr, err := dns.NewRR(fmt.Sprintf("%s. IN A %s", host, staticHost.IP4))
			if err != nil {
				return err
			}
			staticRrs[host] = append(staticRrs[host], rr)
		}
		if staticHost.IP6 != "" {
			rr, err := dns.NewRR(fmt.Sprintf("%s. IN AAAA %s", host, staticHost.IP6))
			if err != nil {
				return err
			}
			staticRrs[host] = append(staticRrs[host], rr)
		}
	}
	fmt.Println("rmap", staticRrs)

	handler := &dnsHandler{}

	// UDP
	go func() {
		mux := dns.NewServeMux()
		for _zone, _rrs := range staticRrs {
			// Copy variables for closure
			zone := _zone
			rrs := _rrs
			mux.HandleFunc(zone+".", func(w dns.ResponseWriter, req *dns.Msg) {
				msg := new(dns.Msg)
				msg.SetReply(req)
				msg.RecursionAvailable = true
				for _, q := range req.Question {
					for _, rr := range rrs {
						if q.Qtype == rr.Header().Rrtype {
							fmt.Println("reply", "q", q, "rr", rr, "zone", zone)
							msg.Answer = append(msg.Answer, rr)
						}
					}
				}
				sendReply(w, req, msg, true)
			})
		}
		mux.HandleFunc(".", func(w dns.ResponseWriter, req *dns.Msg) {
			handler.handleDnsReq(w, req, true)
		})

		server := &dns.Server{PacketConn: udpConn, Handler: mux}
		err := server.ActivateAndServe()
		if err != nil {
			fmt.Println("dns.Server.ActivateAndServe() =", err)
		}
	}()

	// TCP
	go func() {
		mux := dns.NewServeMux()
		for _zone, _rrs := range staticRrs {
			// Copy variables for closure
			zone := _zone
			rrs := _rrs
			mux.HandleFunc(zone+".", func(w dns.ResponseWriter, req *dns.Msg) {
				msg := new(dns.Msg)
				msg.SetReply(req)
				msg.RecursionAvailable = true
				for _, q := range req.Question {
					for _, rr := range rrs {
						if q.Qtype == rr.Header().Rrtype {
							fmt.Println("reply", "q", q, "rr", rr, "zone", zone)
							msg.Answer = append(msg.Answer, rr)
						}
					}
				}
				sendReply(w, req, msg, true)
			})
		}
		mux.HandleFunc(".", func(w dns.ResponseWriter, req *dns.Msg) {
			handler.handleDnsReq(w, req, false)
		})

		server := &dns.Server{Listener: tcpListener, Handler: mux}
		err := server.ActivateAndServe()
		if err != nil {
			fmt.Println("dns.Server.ActivateAndServe() =", err)
		}
	}()

	return nil
}

func mapErrorcode(err error) int {
	switch err {
	case dnssd.ErrNoSuchName:
		return dns.RcodeNameError
	case dnssd.ErrNoSuchRecord:
		return dns.RcodeNameError
	case dnssd.ErrNoAuth:
		return dns.RcodeNotAuth
	case dnssd.ErrRefused:
		return dns.RcodeRefused
	case dnssd.ErrBadTime:
		return dns.RcodeBadTime
	case dnssd.ErrBadSig:
		return dns.RcodeBadSig
	case dnssd.ErrBadKey:
		return dns.RcodeBadKey
	default:
		return dns.RcodeServerFailure
	}
}
