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
	"k8s.io/klog/v2"
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
		klog.Error("w.WriteMsg() =", err)
	}
}

func mapToRR(a dnssd.QueryAnswer) (dns.RR, error) {
	hdr := dns.RR_Header{
		Name:     a.Name,
		Rrtype:   a.Type,
		Class:    a.Class,
		Ttl:      a.TTL,
		Rdlength: uint16(len(a.Data)),
	}
	rr, _, err := dns.UnpackRRWithHeader(hdr, a.Data, 0)
	if err != nil {
		return nil, err
	}
	return rr, nil
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

		answers, err := dnssd.QueryRecursive(q.Name, q.Qtype)
		// First error handling round: try to fix it
		if err != nil {
			klog.Error("dnssd.QueryRecursive() =", err)

			// No network? macOS returns NXDOMAIN but let's return timeout
			if (err == dnssd.ErrNoSuchRecord || err == dnssd.ErrNoSuchName) && netutil.GetDefaultAddress4() == nil {
				return
			}

			// For domains with A but no AAAA (github.com), macOS returns "no such record".
			// musl resolver turns that into a NXDOMAIN response: https://github.com/docker/for-mac/issues/5020
			// Fix: query SOA. if we get a SOA, return it with status=NOERROR. otherwise, return NXDOMAIN if SOA is missing.
			if (err == dnssd.ErrNoSuchRecord || err == dnssd.ErrNoSuchName) && len(answers) == 0 {
				soa, err2 := dnssd.QueryRecursive(q.Name, dns.TypeSOA)
				if err2 == nil {
					// Got SOA. Return it in the *authority* section, not answer section.
					for _, a := range soa {
						rr, err := mapToRR(a)
						if err != nil {
							klog.Error("mapToRR() =", err)
							continue
						}
						msg.Ns = append(msg.Ns, rr)
					}
					err = nil
				}
			}
		}

		// Error handling after SOA logic
		if err != nil {
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
			rr, err := mapToRR(a)
			if err != nil {
				klog.Error("mapToRR() =", err)
				continue
			}
			msg.Answer = append(msg.Answer, rr)
		}
	}

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
			klog.Error("dns.Server.ActivateAndServe() =", err)
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
			klog.Error("dns.Server.ActivateAndServe() =", err)
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
