package main

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/armon/go-radix"
	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

// in the future we should add machines using container.IPAddresses() on .orb.local
var mdnsContainerSuffixes = []string{".docker.local.", ".orb.local."}

const mdnsMachineSuffix = ".orb.local."

const mdnsIndexDomain = "orb.local."

const (
	// short because containers can start/stop often
	mdnsTTL = 60 // seconds

	// matches mDNSResponder timeout
	mdnsProxyTimeout  = 5 * time.Second
	mdnsProxyUpstream = netconf.ServicesIP4 + ":53"
)

type mdnsRegistry struct {
	mu sync.Mutex
	// we store reversed name to do longest prefix match as longest-suffix
	// this allows subdomain wildcards and custom domains to work properly
	tree *radix.Tree

	server *mdns.Server
}

func (r *mdnsRegistry) StartServer(config *mdns.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	server, err := mdns.NewServer(config)
	if err != nil {
		return err
	}
	r.server = server
	return nil
}

func (r *mdnsRegistry) StopServer() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.server != nil {
		err := r.server.Shutdown()
		r.server = nil
		return err
	}
	return nil
}

type mdnsEntry struct {
	// allow *. suffix match? (false for index)
	IsWildcard bool

	// net.IP more efficient b/c dns is in bytes
	ips     []net.IP
	machine *Container
}

func (e mdnsEntry) IPs() []net.IP {
	if e.machine != nil {
		ips, err := e.machine.GetIPAddresses()
		if err != nil {
			logrus.WithError(err).WithField("name", e.machine.Name).Error("failed to get machine IPs for DNS")
			return nil
		}
		return ips
	} else {
		return e.ips
	}
}

func (e mdnsEntry) IsContainer() bool {
	return e.machine == nil
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
		r.tree.Insert(reverse(name), mdnsEntry{
			IsWildcard: true,
			ips:        ips,
		})
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

func (r *mdnsRegistry) AddMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("mdns: add machine")

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tree.Insert(reverse(name), mdnsEntry{
		IsWildcard: true,
		machine:    c,
	})
}

func (r *mdnsRegistry) RemoveMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("mdns: remove machine")

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tree.Delete(reverse(name))
}

func (r *mdnsRegistry) ClearContainers() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tree.Walk(func(s string, v interface{}) bool {
		// delete all container nodes
		entry := v.(mdnsEntry)
		if entry.IsContainer() {
			r.tree.Delete(s)
		}
		return false // continue
	})
}

func (r *mdnsRegistry) Records(q dns.Question, from net.Addr) []dns.RR {
	// top bit = "QU" (unicast) flag
	// mDNSResponder sends QU first. not responding causes 1-sec delay
	qclass := q.Qclass &^ (1 << 15)
	if qclass != dns.ClassINET {
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
		// other types go out to macOS (e.g. for discovery)
		return r.proxyToHost(q, from)
	}

	if !strings.HasSuffix(q.Name, ".local.") {
		return nil // mDNS is only .local
	}

	if verboseDebug { // avoid allocations
		logrus.WithField("name", q.Name).Debug("mdns: lookup")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	treeKey := reverse(q.Name)
	matchedKey, _entry, ok := r.tree.LongestPrefix(treeKey)
	if !ok {
		// not found in local tree, so proxy out to macOS to make a query
		return r.proxyToHost(q, from)
	}
	entry := _entry.(mdnsEntry)
	// if not an exact match: is wildcard allowed?
	if !entry.IsWildcard && matchedKey != treeKey {
		return nil
	}

	var records []dns.RR
	for _, ip := range entry.IPs() {
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

func (r *mdnsRegistry) proxyToHost(q dns.Question, from net.Addr) []dns.RR {
	// to prevent loop: if it's from macOS, don't proxy v4 mDNS
	// works b/c we block v4 multicast in brnet and only send v6, while machines will probably query both
	// TODO: properly check macOS IPv6 link-local addr
	if fromUDP, ok := from.(*net.UDPAddr); ok && fromUDP.IP.To4() == nil {
		return nil
	}

	if verboseDebug {
		logrus.WithField("name", q.Name).Debug("mdns: proxy to host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mdnsProxyTimeout)
	defer cancel()

	// ask host mDNSResponder. it can handle .local queries
	msg := new(dns.Msg)
	msg.SetQuestion(q.Name, q.Qtype)
	msg.RecursionDesired = false // mDNS
	reply, err := dns.ExchangeContext(ctx, msg, mdnsProxyUpstream)
	if err != nil {
		if verboseDebug {
			logrus.WithError(err).WithField("name", q.Name).Debug("host mDNS query failed")
		}
		return nil
	}

	return reply.Answer
}
