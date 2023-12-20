package hostmdns

import (
	"net"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/isclient"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/vmgr/drm"
	"github.com/sirupsen/logrus"
)

type HostMdnsServer struct {
	server *mdns.Server
	drm    *drm.DrmClient
}

func NewHostMdnsServer(drm *drm.DrmClient) (*HostMdnsServer, error) {
	s := &HostMdnsServer{
		drm: drm,
	}

	loopback, err := net.InterfaceByName("lo0")
	if err != nil {
		return nil, err
	}

	// Go disables IP_MULTICAST_LOOP by default
	// TODO how to avoid handling the same query many times?
	server, err := mdns.NewServer(&mdns.Config{
		Zone:  s,
		Iface: loopback,
		// mDNSResponder always sets QU (unicast reply) in questions, but this is needed to make cache flushes work correctly
		// will not cause infinite loop because we only send responses and only handle queries
		Loopback: true,
	})
	if err != nil {
		return nil, err
	}

	s.server = server
	return s, nil
}

func (s *HostMdnsServer) Records(q dns.Question, from net.Addr) []dns.RR {
	// pre-filter to match scon and reduce RPCs: only A, AAAA, ANY
	if q.Qtype != dns.TypeA && q.Qtype != dns.TypeAAAA && q.Qtype != dns.TypeANY {
		return nil
	}

	var rrs []dns.RR
	err := s.drm.UseSconInternalClient(func(scon *isclient.Client) error {
		_rrs, err := scon.MdnsHandleQuery(q)
		if err != nil {
			return err
		}

		rrs = _rrs
		return nil
	})
	if err != nil {
		logrus.WithError(err).Error("mdns: failed to handle query")
		return nil
	}

	return rrs
}

func (s *HostMdnsServer) SendCacheFlush(rrs []dns.RR) error {
	return s.server.SendCacheFlush(rrs)
}
