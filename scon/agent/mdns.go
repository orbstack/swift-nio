package agent

import (
	"net"
	"strings"
	"sync"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

// in the future we should add machines using container.IPAddresses() on .orb.local
var mdnsSuffixes = []string{".docker.local.", ".orb.local."}

const (
	mdnsTTL = 60 // seconds
)

type mdnsRegistry struct {
	mu      sync.Mutex
	entries map[string][]net.IP
}

func newMdnsRegistry() mdnsRegistry {
	return mdnsRegistry{
		entries: make(map[string][]net.IP),
	}
}

func containerToMdnsNames(ctr *dockertypes.ContainerSummaryMin) []string {
	// full ID, short ID, names, compose: service.project
	names := make([]string, 0, 2+len(ctr.Names)+1)
	names = append(names, ctr.ID, ctr.ID[:12])
	for _, name := range ctr.Names {
		names = append(names, strings.TrimPrefix(name, "/"))
	}
	if ctr.Labels != nil {
		if composeProject, ok := ctr.Labels["com.docker.compose.project"]; ok {
			if composeService, ok := ctr.Labels["com.docker.compose.service"]; ok {
				names = append(names, composeService+"."+composeProject)
			}
		}
	}
	return names
}

func containerToMdnsIPs(ctr *dockertypes.ContainerSummaryMin) []net.IP {
	ips := make([]net.IP, 0, len(ctr.NetworkSettings.Networks))
	for _, netSettings := range ctr.NetworkSettings.Networks {
		ip4 := netSettings.IPAddress
		if ip4 != "" {
			ips = append(ips, net.ParseIP(ip4))
		}
		ip6 := netSettings.GlobalIPv6Address
		if ip6 != "" {
			ips = append(ips, net.ParseIP(ip6))
		}
	}
	return ips
}

func (r *mdnsRegistry) AddContainer(ctr *dockertypes.ContainerSummaryMin) {
	names := containerToMdnsNames(ctr)
	ips := containerToMdnsIPs(ctr)
	logrus.WithFields(logrus.Fields{
		"names": names,
		"ips":   ips,
	}).Debug("mdns: add container")

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		if _, ok := r.entries[name]; ok {
			continue
		}
		r.entries[name] = ips
	}
}

func (r *mdnsRegistry) RemoveContainer(ctr *dockertypes.ContainerSummaryMin) {
	names := containerToMdnsNames(ctr)
	logrus.WithField("names", names).Debug("mdns: remove container")

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		delete(r.entries, name)
	}
}

func (r *mdnsRegistry) Records(q dns.Question) []dns.RR {
	if q.Qclass != dns.ClassINET {
		return nil
	}

	includeV4 := false
	includeV6 := false
	switch q.Qtype {
	case dns.TypeANY:
		includeV4 = true
		includeV6 = true
	case dns.TypeA:
		includeV4 = true
	case dns.TypeAAAA:
		includeV6 = true
	default:
		return nil
	}

	var entryName string
	for _, suffix := range mdnsSuffixes {
		if strings.HasSuffix(q.Name, suffix) {
			entryName = strings.TrimSuffix(q.Name, suffix)
			break
		}
	}
	if entryName == "" {
		return nil
	}
	logrus.WithField("entryName", entryName).Debug("mdns: lookup")

	r.mu.Lock()
	defer r.mu.Unlock()
	ips, ok := r.entries[entryName]
	if !ok {
		return nil
	}

	var records []dns.RR
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && includeV4 {
			records = append(records, &dns.A{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    mdnsTTL,
				},
				A: ip4,
			})
		} else if ip6 := ip.To16(); ip6 != nil && includeV6 {
			records = append(records, &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   q.Name,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET,
					Ttl:    mdnsTTL,
				},
				AAAA: ip6,
			})
		}
	}
	return records
}
