package main

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/armon/go-radix"
	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/templates"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

// in the future we should add machines using container.IPAddresses() on .orb.local
var mdnsContainerSuffixes = []string{".orb.local.", ".docker.local."}

const mdnsMachineSuffix = ".orb.local."

const mdnsIndexDomain = "orb.local."

const (
	// long because we have cache flushing on reuse
	// ARP cache is a non-issue. Docker generates MAC from IP within the subnet, so it doesn't change
	mdnsTTLSeconds uint32 = 5 * 60 // = 5 min
	// for wildcard matches, start with a short TTL in case it will be taken by a service that's still starting
	// we're not waiting for the actual server to be ready - this is just when the *container* starts, so it should be fast
	// prevents issues with e.g. docs.orbstack.local matching *.orbstack.local before docs starts
	// if we wildcard-match against the parent twice, it's probably not going to have children
	// no need to keep track of every wildcard query for this
	mdnsInitialWildcardTTLSeconds uint32 = 5

	// flush cache this long after a name was reused
	mdnsCacheFlushDebounce = 250 * time.Millisecond
	// prevent memory leak in case of scanning
	mdnsCacheMaxQueryHistory = 512

	// matches mDNSResponder timeout
	mdnsProxyTimeout  = 5 * time.Second
	mdnsProxyUpstream = netconf.ServicesIP4 + ":53"
)

type mdnsEntry struct {
	// allow *. suffix match? (false for index)
	IsWildcard bool
	// show in index?
	IsHidden bool

	MatchedAsWildcard bool

	Type MdnsEntryType
	// net.IP more efficient b/c dns is in bytes
	ips     []net.IP
	machine *Container
}

type mdnsQueryInfo struct {
	// to check whether flush is necessary, and whether GC is ok
	ExpiresAt time.Time

	// to avoid problems flushing wildcards that were later replaced by a more specific domain,
	// we don't store the matched key, we check the suffix of the qName (map key) instead
}

type mdnsIndexResult struct {
	ContainerDomains []string
	MachineDomains   []string
}

type dnsName struct {
	Name   string
	Hidden bool
}

type MdnsEntryType int

const (
	MdnsEntryContainer MdnsEntryType = iota
	MdnsEntryMachine
	MdnsEntryStatic
)

type mdnsRegistry struct {
	mu sync.Mutex
	// we store reversed name to do longest prefix match as longest-suffix
	// this allows subdomain wildcards and custom domains to work properly
	tree *radix.Tree

	server             *mdns.Server
	cacheFlushDebounce syncx.FuncDebounce
	// must keep track of all queries for cache flushing
	// otherwise we don't know what wildcard queries there could've been
	recentQueries  map[string]mdnsQueryInfo
	pendingFlushes map[string]*mdnsEntry

	host *hclient.Client

	httpServer *http.Server
}

func (r *mdnsRegistry) StartServer(config *mdns.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var err error
	r.server, err = mdns.NewServer(config)
	if err != nil {
		return err
	}

	// start HTTP index server
	r.httpServer = &http.Server{
		Handler: r,
	}
	go runOne("dns index server (v4)", func() error {
		l, err := net.Listen("tcp", net.JoinHostPort(netconf.SconWebIndexIP4, "80"))
		if err != nil {
			return err
		}
		return r.httpServer.Serve(l)
	})
	go runOne("dns index server (v6)", func() error {
		l, err := net.Listen("tcp", net.JoinHostPort(netconf.SconWebIndexIP6, "80"))
		if err != nil {
			return err
		}
		return r.httpServer.Serve(l)
	})

	return nil
}

func (r *mdnsRegistry) listIndexDomains() mdnsIndexResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	var res mdnsIndexResult
	r.tree.Walk(func(s string, v any) bool {
		entry := v.(*mdnsEntry)
		if entry.IsHidden {
			return false
		}

		name := strings.TrimSuffix(reverse(s), ".")
		switch entry.Type {
		case MdnsEntryContainer:
			res.ContainerDomains = append(res.ContainerDomains, name)
		case MdnsEntryMachine:
			res.MachineDomains = append(res.MachineDomains, name)
		}

		return false
	})

	// sort
	sort.Strings(res.ContainerDomains)
	sort.Strings(res.MachineDomains)

	return res
}

func (r *mdnsRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()

	if req.URL.Path != "/" || req.Method != http.MethodGet {
		http.NotFound(w, req)
		return
	}

	// build a list of domains to show
	res := r.listIndexDomains()

	// respond with html
	w.Header().Set("Content-Type", "text/html")
	err := templates.DnsIndexHTML.Execute(w, res)
	if err != nil {
		logrus.WithError(err).Error("failed to execute template")
	}
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

func (e mdnsEntry) ToRecords(qName string, includeV4 bool, includeV6 bool, ttl uint32) []dns.RR {
	var records []dns.RR
	for _, ip := range e.IPs() {
		if ip4 := ip.To4(); ip4 != nil {
			// can't combine chcek because v4 .To16() works
			if includeV4 {
				records = append(records, &dns.A{
					Hdr: dns.RR_Header{
						Name:   qName,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    ttl,
					},
					A: ip4,
				})
			}
		} else if ip6 := ip.To16(); ip6 != nil {
			if includeV6 {
				records = append(records, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   qName,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET,
						Ttl:    ttl,
					},
					AAAA: ip6,
				})
			}
		}
	}

	// if AAAA and no records...
	if includeV6 && !includeV4 && len(records) == 0 {
		// need to send explicit negative response: NSEC
		// https://datatracker.ietf.org/doc/html/rfc6762#section-6.1
		// otherwise macOS delays for several seconds
		records = append(records, &dns.NSEC{
			Hdr: dns.RR_Header{
				Name:   qName,
				Rrtype: dns.TypeNSEC,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			NextDomain: qName,
			TypeBitMap: []uint16{dns.TypeA},
		})
	}

	return records
}

func newMdnsRegistry(host *hclient.Client) *mdnsRegistry {
	r := &mdnsRegistry{
		tree:           radix.New(),
		recentQueries:  make(map[string]mdnsQueryInfo),
		pendingFlushes: make(map[string]*mdnsEntry),
		host:           host,
	}
	r.cacheFlushDebounce = syncx.NewFuncDebounce(mdnsCacheFlushDebounce, r.flushReusedCache)

	// add initial index record
	r.tree.Insert(reverse(mdnsIndexDomain), &mdnsEntry{
		Type:       MdnsEntryStatic,
		IsWildcard: false,
		IsHidden:   true, // don't show itself
		ips: []net.IP{
			net.ParseIP(netconf.SconWebIndexIP4),
			net.ParseIP(netconf.SconWebIndexIP6),
		},
	})

	return r
}

func reverse(s string) string {
	// simply reversing the entire thing is fine - as long as we do it consistently
	buf := make([]byte, 0, len(s))
	for i := len(s) - 1; i >= 0; i-- {
		buf = append(buf, s[i])
	}
	return string(buf)
}

func (r *mdnsRegistry) containerToMdnsNames(ctr *dockertypes.ContainerSummaryMin, notifyInvalid bool) []dnsName {
	// (3) short ID, names, compose: service.project
	// full ID is too long for DNS: it's 64 chars, max is 63 per component
	names := make([]dnsName, 0, 1+len(ctr.Names)+1)
	// full ID is always hidden
	names = append(names, dnsName{ctr.ID[:12], true})
	for _, name := range ctr.Names {
		names = append(names, dnsName{strings.TrimPrefix(name, "/"), false})
	}
	if ctr.Labels != nil {
		if composeProject, ok := ctr.Labels["com.docker.compose.project"]; ok {
			if composeService, ok := ctr.Labels["com.docker.compose.service"]; ok {
				// if we have a compose name, mark all the default ones as hidden
				for i := range names {
					names[i].Hidden = true
				}
				names = append(names, dnsName{composeService + "." + composeProject, false})
			}
		}
	}

	// all names above should have suffixes appended
	for i, name := range names {
		for j, suffix := range mdnsContainerSuffixes {
			// reuse existing array element for first suffix
			if j == 0 {
				names[i] = dnsName{name.Name + suffix, name.Hidden}
			} else {
				// alias suffixes are always hidden
				names = append(names, dnsName{name.Name, true})
			}
		}
	}

	if ctr.Labels != nil {
		if extraNames, ok := ctr.Labels["dev.orbstack.domains"]; ok && extraNames != "" {
			// if we have extra names, mark all the default ones as hidden
			for i := range names {
				names[i].Hidden = true
			}

			for _, name := range strings.Split(extraNames, ",") {
				if !strings.HasSuffix(name, ".") {
					name += "."
				}

				// only validate user-provided domains
				if ok, reason := validateName(name); !ok {
					logrus.WithField("name", name).WithField("reason", reason).Error("invalid custom domain")
					// send notification
					if notifyInvalid {
						go func() {
							err := r.host.Notify(guitypes.Notification{
								Title:   "Invalid domain: " + strings.TrimSuffix(name, "."),
								Message: reason,
								Silent:  true,
								URL:     "https://docs.orbstack.dev/readme-link/invalid-container-domain",
							})
							if err != nil {
								logrus.WithError(err).Error("failed to send notification")
							}
						}()
					}

					continue
				}

				names = append(names, dnsName{name, false})
			}
		}
	}

	return names
}

// string so gofmt doesn't complain about capital
func validateName(name string) (bool, string) {
	if !strings.HasSuffix(name, ".local.") {
		return false, "Must end with .local"
	}
	parts := strings.Split(name, ".")
	for i, part := range parts {
		if len(part) > 63 {
			return false, "Each component must be under 63 characters"
		}
		// last part can be empty
		if len(part) == 0 && i != len(parts)-1 {
			return false, "Empty component"
		}
	}
	if len(name) > 255 {
		return false, "Must be under 255 characters"
	}
	return true, ""
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

// flush cache of all reused names that were queried
// must record queries because of wildcards: we don't know what wildcard subdomains the user may have queried
// and to prevent overflowing MTU, don't flush every possible name/alias unless it was actually used
func (r *mdnsRegistry) flushReusedCache() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.server == nil {
		return
	}

	// send cache flush: prepopulate cache with new reused names
	// no point in checking if IPs changed - we're just updating cache with the same values
	flushRecords := make([]dns.RR, 0, len(r.pendingFlushes))
	for qName, entry := range r.pendingFlushes {
		flushRecords = append(flushRecords, entry.ToRecords(qName, true, true, mdnsTTLSeconds)...)
	}
	if len(flushRecords) > 0 {
		if verboseDebug {
			logrus.WithField("records", flushRecords).Debug("mdns: sending cache flush")
		}

		// careful: if any records are invalid, this will fail with rrdata error
		// but it's ok: cache flushing is based on queried records.
		// if a name is invalid (>63 component / >255), it's not possible to query it
		err := r.server.SendCacheFlush(flushRecords)
		if err != nil {
			logrus.WithError(err).Error("failed to flush cache")
		}
	}

	// GC: remove records with expired TTL
	// this is OK from VM's monotonic time perspective, because VM time will never advance faster than host
	now := time.Now()
	for qName, qInfo := range r.recentQueries {
		if now.After(qInfo.ExpiresAt) {
			delete(r.recentQueries, qName)
		}
	}

	// reset pending flushes
	// TODO use Go 1.21 clear
	for k := range r.pendingFlushes {
		delete(r.pendingFlushes, k)
	}
}

func (r *mdnsRegistry) maybeFlushCacheLocked(now time.Time, changedName string, newEntry *mdnsEntry) {
	for qName, qInfo := range r.recentQueries {
		if now.Before(qInfo.ExpiresAt) && strings.HasSuffix(qName, changedName) {
			// if no new entry was provided, look for one in the tree
			// this lets us flush cache back to a wildcard if the more-specific domain was removed
			// we're required to send latest known info when flushing cache,
			// because otherwise it'll leave a negative cache entry in client
			if newEntry == nil {
				_, _entry, ok := r.tree.LongestPrefix(reverse(qName))
				if !ok {
					continue
				}
				newEntry = _entry.(*mdnsEntry)
			}

			r.pendingFlushes[qName] = newEntry
			r.cacheFlushDebounce.Call()
		}
	}
}

func (r *mdnsRegistry) AddContainer(ctr *dockertypes.ContainerSummaryMin) {
	names := r.containerToMdnsNames(ctr, true /*notifyInvalid*/)
	ips := containerToMdnsIPs(ctr)
	logrus.WithFields(logrus.Fields{
		"names": names,
		"ips":   ips,
	}).Debug("mdns: add container")

	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, name := range names {
		entry := &mdnsEntry{
			Type:       MdnsEntryContainer,
			IsWildcard: true,
			// short-ID and aliases are hidden, real names and custom names are not
			IsHidden: name.Hidden,
			ips:      ips,
		}
		treeKey := reverse(name.Name)
		r.tree.Insert(treeKey, entry)

		// need to flush any caches? what names were we queried under? (wildcard)
		r.maybeFlushCacheLocked(now, name.Name, entry)
	}
}

func (r *mdnsRegistry) RemoveContainer(ctr *dockertypes.ContainerSummaryMin) {
	names := r.containerToMdnsNames(ctr, false /*notifyInvalid*/)
	logrus.WithField("names", names).Debug("mdns: remove container")

	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, name := range names {
		r.tree.Delete(reverse(name.Name))
		r.maybeFlushCacheLocked(now, name.Name, nil)
	}
}

func (r *mdnsRegistry) AddMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("mdns: add machine")

	r.mu.Lock()
	defer r.mu.Unlock()

	// we don't validate these b/c it's not under the user's control
	treeKey := reverse(name)
	entry := &mdnsEntry{
		Type:       MdnsEntryMachine,
		IsWildcard: true,
		// machines only have one name, but hide docker
		IsHidden: c.builtin,
		machine:  c,
	}
	r.tree.Insert(treeKey, entry)

	// need to flush any caches? what names were we queried under? (wildcard)
	r.maybeFlushCacheLocked(time.Now(), name, entry)
}

func (r *mdnsRegistry) RemoveMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("mdns: remove machine")

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tree.Delete(reverse(name))
	r.maybeFlushCacheLocked(time.Now(), name, nil)
}

func (r *mdnsRegistry) ClearContainers() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tree.Walk(func(s string, v interface{}) bool {
		// delete all container nodes
		entry := v.(*mdnsEntry)
		if entry.Type == MdnsEntryContainer {
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
	if verboseDebug {
		logrus.WithFields(logrus.Fields{
			"treeKey":    treeKey,
			"matchedKey": matchedKey,
			"entry":      _entry,
			"ok":         ok,
		}).Debug("mdns: lookup result")
	}
	if !ok {
		// not found in local tree, so proxy out to macOS to make a query
		return r.proxyToHost(q, from)
	}
	entry := _entry.(*mdnsEntry)

	// if not an exact match: is wildcard allowed?
	ttlSeconds := mdnsTTLSeconds
	if matchedKey != treeKey {
		// this was a wildcard match. is that allowed?
		if !entry.IsWildcard {
			return nil
		}

		// make sure we're matching on a component boundary:
		// check that the next character is a dot
		// e.g. stack.local shouldn't match orbstack.local
		if len(treeKey) > len(matchedKey) && treeKey[len(matchedKey)] != '.' {
			return nil
		}

		// only allow one wildcard component, not *.*.*.
		// do this by counting dots and making sure there's no more than one extra dot
		if strings.Count(treeKey, ".") > strings.Count(matchedKey, ".")+1 {
			return nil
		}

		// note: we do *NOT* check whether the matched key was a leaf node (i.e. has no children)
		// because expected behavior for wildcards (at least explicit *. ones) is precisely to match
		// against a more specific child if available, and if not, fall back to the wildcard parent

		// track initial cache period TTL
		if !entry.MatchedAsWildcard {
			ttlSeconds = mdnsInitialWildcardTTLSeconds
		}
		entry.MatchedAsWildcard = true
	}

	records := entry.ToRecords(q.Name, includeV4, includeV6, ttlSeconds)
	if len(records) == 0 {
		return nil
	}

	// record query for cache flushing
	if len(r.recentQueries) >= mdnsCacheMaxQueryHistory {
		// delete a random entry to stay under limit
		// very unlikely to hit this in real usage - rare enough that LRU isn't worth it
		for k := range r.recentQueries {
			delete(r.recentQueries, k)
			break
		}
	}
	r.recentQueries[q.Name] = mdnsQueryInfo{
		ExpiresAt: time.Now().Add(time.Duration(ttlSeconds) * time.Second),
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
	// don't think vnet supports fragmentation. this value is from dig
	msg.SetEdns0(1232, false)
	reply, err := dns.ExchangeContext(ctx, msg, mdnsProxyUpstream)
	if err != nil {
		if verboseDebug {
			logrus.WithError(err).WithField("name", q.Name).Debug("host mDNS query failed")
		}
		return nil
	}

	return reply.Answer
}
