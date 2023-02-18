package dnssrv

import (
	"fmt"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/netutil"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/dns/dnssd"
	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

var (
	verboseTrace = conf.Debug()
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
		logrus.Error("w.WriteMsg() =", err)
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

func mapFallbackQtype(qtype uint16) uint16 {
	switch qtype {
	case dns.TypeAAAA:
		return dns.TypeA
	case dns.TypeA:
		return dns.TypeAAAA
	case dns.TypeCNAME:
		return 0 // not needed. macOS returns CNAME
	default:
		return dns.TypeA // most common
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

		if verboseTrace {
			logrus.WithFields(logrus.Fields{
				"name": q.Name,
				"type": dns.TypeToString[q.Qtype],
			}).Trace("start handling DNS query")
		}
		answers, err := dnssd.QueryRecursive(q.Name, q.Qtype)
		if verboseTrace {
			logrus.WithFields(logrus.Fields{
				"name": q.Name,
				"type": dns.TypeToString[q.Qtype],
				"ans":  answers,
				"err":  err,
			}).Trace("got first answer from QueryRecursive()")
		}
		// First error handling round: try to fix it
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"name":  q.Name,
				"type":  dns.TypeToString[q.Qtype],
				"error": err,
			}).Error("QueryRecursive() failed")
			isNxdomain := (err == dnssd.ErrNoSuchRecord || err == dnssd.ErrNoSuchName)

			// No network? macOS returns NXDOMAIN, we return timeout
			if isNxdomain && netutil.GetDefaultAddress4() == nil && netutil.GetDefaultAddress6() == nil {
				if verboseTrace {
					logrus.WithFields(logrus.Fields{
						"name": q.Name,
						"type": dns.TypeToString[q.Qtype],
					}).Trace("no network, returning timeout")
				}
				return
			}

			// For domains with A but no AAAA (github.com), macOS returns "no such record".
			// musl resolver turns that into a NXDOMAIN response: https://github.com/docker/for-mac/issues/5020
			// Fix: query SOA. if we get a SOA, return it with status=NOERROR. otherwise, return NXDOMAIN if SOA is missing.
			fallbackQtype := mapFallbackQtype(q.Qtype)
			if isNxdomain && len(answers) == 0 && fallbackQtype != 0 {
				if verboseTrace {
					logrus.WithFields(logrus.Fields{
						"name":     q.Name,
						"type":     dns.TypeToString[q.Qtype],
						"fallback": dns.TypeToString[fallbackQtype],
					}).Trace("trying fallback query")
				}
				_, err2 := dnssd.QueryRecursive(q.Name, fallbackQtype)
				if verboseTrace {
					logrus.WithFields(logrus.Fields{
						"name":     q.Name,
						"type":     dns.TypeToString[q.Qtype],
						"fallback": dns.TypeToString[fallbackQtype],
						"err":      err2,
					}).Trace("got fallback answer")
				}
				if err2 == nil {
					// we got something, so it's not NXDOMAIN
					err = nil
				}
			}

			// If we got "no such record" while resolving CNAME but actually got CNAME records, return them.
			if isNxdomain && len(answers) > 0 {
				// Clear the error.
				err = nil
			}
		}

		// Error handling after fallback logic
		if err != nil {
			if verboseTrace {
				logrus.WithFields(logrus.Fields{
					"name": q.Name,
					"type": dns.TypeToString[q.Qtype],
					"err":  err,
				}).Trace("got error after fallback logic")
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
			if verboseTrace {
				logrus.WithFields(logrus.Fields{
					"name": q.Name,
					"type": dns.TypeToString[q.Qtype],
					"ans":  a,
				}).Trace("returning answer")
			}
			rr, err := mapToRR(a)
			if err != nil {
				logrus.Error("mapToRR() =", err)
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
		Port: ports.ServiceDNS,
	}, nil, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	tcpListener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.ServiceDNS,
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
			logrus.Error("dns.Server.ActivateAndServe() =", err)
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
			logrus.Error("dns.Server.ActivateAndServe() =", err)
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
