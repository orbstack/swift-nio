package agent

import (
	"net"
	"strings"
	"sync"

	"github.com/armon/go-radix"
	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

// in the future we should add machines using container.IPAddresses() on .orb.local
var mdnsContainerSuffixes = []string{".docker.local.", ".orb.local."}

// exclude macOS cache flush probes
var mdnsExcludeSuffixes = []string{"._tcp.local.", "._udp.local."}

const (
	// short because containers can start/stop often
	mdnsTTL = 60 // seconds
)

type mdnsRegistry struct {
	mu sync.Mutex
	// we store reversed name to do longest prefix match as longest-suffix
	// this allows subdomain wildcards and custom domains to work properly
	tree *radix.Tree
}

type mdnsEntry struct {
	// net.IP more efficient b/c dns is in bytes
	IPs []net.IP
}

func newMdnsRegistry() mdnsRegistry {
	return mdnsRegistry{
		tree: radix.New(),
	}
}

func reverse(s string) string {
	// simply reversing the entire thing is fine - as long as we do it consistently
	buf := make([]byte, 0, len(s))
	for i := len(s) - 1; i >= 0; i-- {
		buf = append(buf, s[i])
	}
	return string(buf)
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

	// all names above should have suffixes appended
	for i, name := range names {
		for j, suffix := range mdnsContainerSuffixes {
			// reuse existing array element for first suffix
			if j == 0 {
				names[i] = name + suffix
			} else {
				names = append(names, name+suffix)
			}
		}
	}

	if ctr.Labels != nil {
		if extraNames, ok := ctr.Labels["dev.orbstack.domains"]; ok {
			for _, name := range strings.Split(extraNames, ",") {
				if !strings.HasSuffix(name, ".") {
					name += "."
				}
				if !strings.HasSuffix(name, ".local.") {
					logrus.WithField("name", name).Warn("dev.orbstack.domains: ignoring non-local domain")
				}
				names = append(names, name)
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
		r.tree.Insert(reverse(name), mdnsEntry{IPs: ips})
	}
}

func (r *mdnsRegistry) RemoveContainer(ctr *dockertypes.ContainerSummaryMin) {
	names := containerToMdnsNames(ctr)
	logrus.WithField("names", names).Debug("mdns: remove container")

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		r.tree.Delete(reverse(name))
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

	if !strings.HasSuffix(q.Name, ".local.") {
		return nil // mDNS is only .local
	}
	// exclude macOS cache flush probes
	for _, suffix := range mdnsExcludeSuffixes {
		if strings.HasSuffix(q.Name, suffix) {
			return nil
		}
	}

	logrus.WithField("name", q.Name).Debug("mdns: lookup")

	r.mu.Lock()
	defer r.mu.Unlock()
	_, _entry, ok := r.tree.LongestPrefix(reverse(q.Name))
	if !ok {
		return nil
	}
	entry := _entry.(mdnsEntry)

	var records []dns.RR
	for _, ip := range entry.IPs {
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
