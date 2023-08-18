package main

import (
	"context"
	"net"
	"net/http"
	"net/netip"
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
	mdnsTTLSeconds = 5 * 60 // = 5 min

	// flush cache this long after a name was reused
	mdnsCacheFlushDebounce = 250 * time.Millisecond
	// prevent memory leak in case of scanning
	mdnsCacheMaxQueryHistory = 512

	// matches mDNSResponder timeout
	mdnsProxyTimeout  = 5 * time.Second
	mdnsProxyUpstream = netconf.ServicesIP4 + ":53"

	mdnsCacheFlushRrclass = 1 << 15 // top bit
)

var nat64Prefix = netip.MustParsePrefix(netconf.NAT64Subnet6CIDR)

type mdnsEntry struct {
	// allow *. suffix match? (false for index)
	IsWildcard bool
	// show in index?
	IsHidden bool

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
	Name     string
	Hidden   bool
	Wildcard bool
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
	pendingFlushes map[string]struct{}

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
		l, err := net.Listen("tcp4", net.JoinHostPort(netconf.SconWebIndexIP4, "80"))
		if err != nil {
			return err
		}
		return r.httpServer.Serve(l)
	})
	go runOne("dns index server (v6)", func() error {
		// breaks with DAD on bridge interface
		l, err := net.Listen("tcp6", net.JoinHostPort(netconf.SconWebIndexIP6, "80"))
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

	// don't sort. radix tree is pre-sorted in a good order, by suffix
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
	ips := e.IPs()

	// A
	if includeV4 {
		for _, ip := range ips {
			if ip4 := ip.To4(); ip4 != nil {
				// can't combine check bexcause v4 .To16() works
				records = append(records, &dns.A{
					Hdr: dns.RR_Header{
						Name:   qName,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET | mdnsCacheFlushRrclass,
						Ttl:    ttl,
					},
					A: ip4,
				})
			}
		}
	}

	// AAAA
	if includeV6 {
		var gotIP6 bool
		for _, ip := range ips {
			if ip.To4() == nil {
				ip6 := ip.To16()
				records = append(records, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   qName,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET | mdnsCacheFlushRrclass,
						Ttl:    ttl,
					},
					AAAA: ip6,
				})
				gotIP6 = true
			}
		}

		// if we got none, use NAT64 address derived from IPv4
		// this helps for several reasons:
		//   - Safari (Network.framework) uses interface scoped-address for v4 mDNS response so it can't connect, but it doesn't do scope for v6
		//   - scon machine IPv6 isn't going to conflict with anything, unlike IPv4 and Docker bridges
		//   - we get multi-second delays when returning NSEC for AAAA (due to some unknown changes). returning both is fine
		if !gotIP6 {
			for _, ip := range ips {
				if ip4 := ip.To4(); ip4 != nil {
					// map NAT64 /96
					ip6 := nat64Prefix.Addr().AsSlice()
					copy(ip6[12:], ip4)

					records = append(records, &dns.AAAA{
						Hdr: dns.RR_Header{
							Name:   qName,
							Rrtype: dns.TypeAAAA,
							Class:  dns.ClassINET | mdnsCacheFlushRrclass,
							Ttl:    ttl,
						},
						AAAA: ip6[:],
					})
				}
			}
		}
	}

	return records
}

func newMdnsRegistry(host *hclient.Client) *mdnsRegistry {
	r := &mdnsRegistry{
		tree:           radix.New(),
		recentQueries:  make(map[string]mdnsQueryInfo),
		pendingFlushes: make(map[string]struct{}),
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
	// all default domains are wildcards, b/c we don't set them up in a hierarchy so they can't conflict
	names = append(names, dnsName{Name: ctr.ID[:12], Hidden: true, Wildcard: true})
	for _, name := range ctr.Names {
		names = append(names, dnsName{Name: strings.TrimPrefix(name, "/"), Hidden: false, Wildcard: true})
	}
	if ctr.Labels != nil {
		if composeProject, ok := ctr.Labels["com.docker.compose.project"]; ok {
			if composeService, ok := ctr.Labels["com.docker.compose.service"]; ok {
				// if we have a compose name, mark all the default ones as hidden
				for i := range names {
					names[i].Hidden = true
				}
				names = append(names, dnsName{Name: composeService + "." + composeProject, Hidden: false, Wildcard: true})
			}
		}
	}

	// all names above should have suffixes appended
	for i, name := range names {
		for j, suffix := range mdnsContainerSuffixes {
			// reuse existing array element for first suffix
			if j == 0 {
				names[i].Name += suffix
			} else {
				// alias suffixes are always hidden
				names = append(names, dnsName{Name: name.Name, Hidden: true, Wildcard: true})
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
				// wildcard?
				// default off due to ambiguous cases that cause problems depending on service startup order,
				// e.g. orbstack.local and docs.orbstack.local
				isWildcard := false
				if strings.HasPrefix(name, "*.") {
					name = strings.TrimPrefix(name, "*.")
					isWildcard = true
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
								URL:     "https://go.orbstack.dev/invalid-container-domain",
							})
							if err != nil {
								logrus.WithError(err).Error("failed to send notification")
							}
						}()
					}

					continue
				}

				names = append(names, dnsName{Name: name, Hidden: false, Wildcard: isWildcard})
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
	for qName := range r.pendingFlushes {
		// easy to get correct new records by just querying again
		// note: macOS doesn't respect NSEC flush to indicate "no longer exists"
		qRecords := r.getRecordsLocked(dns.Question{
			Name:   qName,
			Qtype:  dns.TypeANY,
			Qclass: dns.ClassINET,
		}, true, true)
		flushRecords = append(flushRecords, qRecords...)
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
	clear(r.pendingFlushes)
}

func (r *mdnsRegistry) maybeFlushCacheLocked(now time.Time, changedName string) {
	for qName, qInfo := range r.recentQueries {
		if now.Before(qInfo.ExpiresAt) && strings.HasSuffix(qName, changedName) {
			// too hard to figure out what the new records should be at this point, so just use the query code path
			r.pendingFlushes[qName] = struct{}{}
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
			IsWildcard: name.Wildcard,
			// short-ID and aliases are hidden, real names and custom names are not
			IsHidden: name.Hidden,
			ips:      ips,
		}
		treeKey := reverse(name.Name)
		r.tree.Insert(treeKey, entry)

		// need to flush any caches? what names were we queried under? (wildcard)
		r.maybeFlushCacheLocked(now, name.Name)
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
		r.maybeFlushCacheLocked(now, name.Name)
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
	r.maybeFlushCacheLocked(time.Now(), name)
}

func (r *mdnsRegistry) RemoveMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("mdns: remove machine")

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tree.Delete(reverse(name))
	r.maybeFlushCacheLocked(time.Now(), name)
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

// NSEC = explicit negative response
// lets client to return NXDOMAIN immediately instead of hanging
// https://datatracker.ietf.org/doc/html/rfc6762#section-6.1
// otherwise macOS delays for several seconds if AAAA missing
func nxdomain(q dns.Question) []dns.RR {
	// flush all for synthetic ANY query
	var qTypes []uint16
	if q.Qtype == dns.TypeANY {
		// bits must be sorted
		qTypes = []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeANY}
	} else {
		qTypes = []uint16{q.Qtype}
	}

	return []dns.RR{&dns.NSEC{
		Hdr: dns.RR_Header{
			Name:   q.Name,
			Rrtype: dns.TypeNSEC,
			Class:  dns.ClassINET | mdnsCacheFlushRrclass,
			// this is OK because we record it in cache-flush query history
			Ttl: mdnsTTLSeconds,
		},
		NextDomain: q.Name,
		TypeBitMap: qTypes,
	}}
}

// dispatcher, to either host proxy or our server
func (r *mdnsRegistry) Records(q dns.Question, from net.Addr) []dns.RR {
	// top bit = "QU" (unicast) flag
	// mDNSResponder sends QU first. not responding causes 1-sec delay
	qclass := q.Qclass &^ (1 << 15)
	if qclass != dns.ClassINET {
		return nil
	}

	// this is a dual-purpose server:
	//   - if query came from Linux, proxy out to macOS host and let mDNSResponder take care of it
	//     * this is OK because domains handled by us will just loop back. less efficient, but much simpler
	//   - if query came from macOS, we're the authoritative server
	//
	// this prevents looping to/from macOS,
	// check by blocking v6: works b/c we block v4 multicast in brnet and only send v6, while machines will probably query both
	// TODO: properly check macOS IPv6 link-local addr
	if fromUDP, ok := from.(*net.UDPAddr); ok && fromUDP.IP.To4() != nil {
		// this query is from Linux because it's IPv4
		return r.proxyToHost(q)
	} else {
		// this query is from macOS because it's IPv6
		return r.handleQuery(q)
	}
}

// authoritative server
func (r *mdnsRegistry) handleQuery(q dns.Question) []dns.RR {
	// only handle A, AAAA, and ANY
	// TODO respond to Safari's HTTPS/OPT with NSEC?
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

	if verboseDebug { // avoid allocations
		logrus.WithField("name", q.Name).Debug("mdns: lookup")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// do lookup, and generate NSEC if necessary
	records := r.getRecordsLocked(q, includeV4, includeV6)

	// record query for cache flushing
	// this include NSEC so that we can flush negative cache if added later
	if len(r.recentQueries) >= mdnsCacheMaxQueryHistory {
		// delete a random entry to stay under limit
		// very unlikely to hit this in real usage - rare enough that LRU isn't worth it
		for k := range r.recentQueries {
			delete(r.recentQueries, k)
			break
		}
	}
	r.recentQueries[q.Name] = mdnsQueryInfo{
		ExpiresAt: time.Now().Add(mdnsTTLSeconds * time.Second),
	}

	return records
}

// the idea: we return NSEC if not found AND we know we're in control of the name
// that means we either got a tree match but rejected it, or it's under our suffix
func (r *mdnsRegistry) getRecordsLocked(q dns.Question, includeV4 bool, includeV6 bool) []dns.RR {
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
		// no match at all.
		// return NSEC only if it's under our main suffix
		// otherwise we can't take responsibility for this name
		if strings.HasSuffix(q.Name, mdnsContainerSuffixes[0]) {
			return nxdomain(q)
		} else {
			return nil
		}
	}
	entry := _entry.(*mdnsEntry)

	// if not an exact match: is wildcard allowed?
	if matchedKey != treeKey {
		// this was a wildcard match. is that allowed?
		if !entry.IsWildcard {
			return nxdomain(q)
		}

		// make sure we're matching on a component boundary:
		// check that the next character is a dot
		// e.g. stack.local shouldn't match orbstack.local
		if len(treeKey) > len(matchedKey) && treeKey[len(matchedKey)] != '.' {
			return nxdomain(q)
		}

		// only allow one wildcard component, not *.*.*.
		// do this by counting dots and making sure there's no more than one extra dot
		if strings.Count(treeKey, ".") > strings.Count(matchedKey, ".")+1 {
			return nxdomain(q)
		}

		// note: we do *NOT* check whether the matched key was a leaf node (i.e. has no children)
		// because expected behavior for wildcards (at least explicit *. ones) is precisely to match
		// against a more specific child if available, and if not, fall back to the wildcard parent

		// no need to use a shorter cache TTL for initial wildcard queries.
		// we handle it by flushing cache
		// Chrome caches DNS anyway so the short TTL doesn't help
	}

	records := entry.ToRecords(q.Name, includeV4, includeV6, mdnsTTLSeconds)
	if len(records) == 0 {
		// no records, return NSEC b/c we still got a match
		return nxdomain(q)
	}

	return records
}

func (r *mdnsRegistry) proxyToHost(q dns.Question) []dns.RR {
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
