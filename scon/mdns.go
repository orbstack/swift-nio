package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/armon/go-radix"
	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/agent/tlsutil"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/mdns"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/templates"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// in the future we should add machines using container.IPAddresses() on .orb.local
// we don't do .docker.local anymore - no one used it
var mdnsContainerSuffixes = []string{".orb.local."}

const mdnsMachineSuffix = ".orb.local."

const mdnsIndexDomain = "orb.local"

const (
	// long because we have cache flushing on reuse
	// ARP cache is a non-issue. Docker generates MAC from IP within the subnet, so it doesn't change
	mdnsTTLSeconds    = 5 * 60 // = 5 min
	kubeDnsTTLSeconds = 5      // = 5 sec (default kube-dns)

	// flush cache this long after a name was reused
	mdnsCacheFlushDebounce = 250 * time.Millisecond
	// prevent memory leak in case of scanning
	mdnsCacheMaxQueryHistory = 512

	// matches mDNSResponder timeout
	mdnsProxyTimeout  = 5 * time.Second
	mdnsProxyUpstream = netconf.VnetServicesIP4 + ":53"

	mdnsCacheFlushRrclass = 1 << 15 // top bit
)

var (
	nat64Prefix              = netip.MustParsePrefix(netconf.NAT64Subnet6CIDR)
	domainproxySubnet4Prefix = netip.MustParsePrefix(netconf.DomainproxySubnet4Cidr)

	sconHostBridgeIp4 = net.ParseIP(netconf.SconHostBridgeIP4)
	sconHostBridgeIp6 = net.ParseIP(netconf.SconHostBridgeIP6)
)

func mustParseCIDR(s string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return ipnet
}

type mdnsEntry struct {
	r *mdnsRegistry

	// should match the one for mdns
	id string

	// allow *. suffix match? (false for index)
	IsWildcard bool
	// show in index?
	IsHidden bool

	Type MdnsEntryType

	// net.IP more efficient b/c dns is in bytes

	ip4 net.IP
	ip6 net.IP

	owningMachine   *Container
	owningDockerCid string
}

type mdnsQueryInfo struct {
	// to check whether flush is necessary, and whether GC is ok
	ExpiresAt time.Time

	// to avoid problems flushing wildcards that were later replaced by a more specific domain,
	// we don't store the matched key, we check the suffix of the qName (map key) instead
}

type mdnsIndexResult struct {
	Proto            string
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

type domainproxyInfo struct {
	// maps domainproxy ips to container ips. we call container ips values
	ipMap map[netip.Addr]net.IP

	// maps mdns-ids (concatenation of sorted hosts with ,) to domainproxy ips
	idMap4     map[string]netip.Addr
	ipsFull4   bool
	subnet4    netip.Prefix
	lowest4    netip.Addr
	lastAlloc4 netip.Addr

	idMap6     map[string]netip.Addr
	ipsFull6   bool
	subnet6    netip.Prefix
	lowest6    netip.Addr
	lastAlloc6 netip.Addr
}

func mustAddrFromSlice(ip net.IP) netip.Addr {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		panic("failed to convert net.IP into netip.Addr")
	}
	return addr
}

func newDomainproxyInfo(subnet netip.Prefix, lowest netip.Addr) domainproxyInfo {
	return domainproxyInfo{
		ipMap: make(map[netip.Addr]net.IP),

		idMap4:     make(map[string]netip.Addr),
		ipsFull4:   false,
		subnet4:    subnet.Masked(),
		lowest4:    lowest,
		lastAlloc4: lowest,
	}
}

func (d *domainproxyInfo) setAddr(ip netip.Addr, val net.IP) {
	if val == nil {
		d.setAddrFreeable(ip)
		return
	}

	if ip.Is4() {
		nft.Run("add", "element", "inet", "vm", "domainproxy4", fmt.Sprintf("{ %v : %v }", ip, val))
	}
	if ip.Is6() {
		nft.Run("add", "element", "inet", "vm", "domainproxy6", fmt.Sprintf("{ %v : %v }", ip, val))
	}

	d.ipMap[ip] = val
	logrus.WithFields(logrus.Fields{"ip": ip, "val": val}).Debug("emmie | set addr.")
}

func (d *domainproxyInfo) setAddrFreeable(ip netip.Addr) {
	if _, has := d.ipMap[ip]; has {
		d.ipMap[ip] = nil

		if ip.Is4() {
			nft.Run("add", "element", "inet", "vm", "domainproxy4", fmt.Sprintf("{ %v }", ip))
		}
		if ip.Is6() {
			nft.Run("add", "element", "inet", "vm", "domainproxy6", fmt.Sprintf("{ %v }", ip))
		}

		logrus.WithField("ip", ip).Debug("emmie | set addr freeable.")
	}
}

func (d *domainproxyInfo) setIp(ip net.IP, val net.IP) {
	d.setAddr(mustAddrFromSlice(ip), val)
}

func (d *domainproxyInfo) setIpFreeable(ip net.IP) {
	d.setAddrFreeable(mustAddrFromSlice(ip))
}

func (d *domainproxyInfo) getAddr(ip netip.Addr) (val net.IP, has bool) {
	val, has = d.ipMap[ip]
	return val, has
}

func (d *domainproxyInfo) getIp(ip net.IP) (val net.IP, has bool) {
	return d.getAddr(mustAddrFromSlice(ip))
}

// needs a mutex
func nextAvailableIp(ipMap map[netip.Addr]net.IP, subnet netip.Prefix, lowest netip.Addr, lastAlloc *netip.Addr, ipsFull *bool) (ip netip.Addr, ok bool) {
	ip = *lastAlloc

	var freeableIp netip.Addr
	foundFreeableIp := false

	for {
		ip = ip.Next()

		// wrap around
		if !subnet.Contains(ip) {
			ip = lowest
		}

		val, has := ipMap[ip]
		if !has {
			*lastAlloc = ip
			return ip, true
		}

		// freeable ips are zero ips. they can be reclaimed but we're hoping to reuse them for the domain they were used for originally
		if !foundFreeableIp && val == nil {
			freeableIp = ip
			foundFreeableIp = true
			// we already know that we're not gonna find any free spots. take our freeable and run!
			if *ipsFull {
				break
			}
		}

		// we wrapped all the way around, we're out of ips
		if ip == *lastAlloc {
			*ipsFull = true
			break
		}
	}

	if foundFreeableIp {
		return freeableIp, true
	} else {
		return ip, false
	}
}

// needs mutex
func (d *domainproxyInfo) claimNextAvailableIp4(id string, val net.IP) (ip netip.Addr, ok bool) {
	if preferredAddr, has := d.idMap4[id]; has {
		if preferredAddrVal, has := d.getAddr(preferredAddr); has && preferredAddrVal == nil {
			d.setAddr(preferredAddr, val)
			// id map already has the right value
			logrus.WithFields(logrus.Fields{"id": id, "ip": preferredAddr, "val": val}).Debug("emmie | claimed preferred ip.")
			return preferredAddr, true
		} else {
			logrus.WithField("preferredAddr", preferredAddr).Debug("could not assign preferred ip.")
		}
	}

	if nextAddr, ok := nextAvailableIp(d.ipMap, d.subnet4, d.lowest4, &d.lastAlloc4, &d.ipsFull4); ok {
		d.setAddr(nextAddr, val)
		d.idMap4[id] = nextAddr

		logrus.WithFields(logrus.Fields{"id": id, "ip": nextAddr, "val": val}).Debug("emmie | claimed available ip.")
		return nextAddr, true
	}

	logrus.WithFields(logrus.Fields{"id": id, "val": val}).Debug("emmie | failed to claim an ip")
	return netip.Addr{}, false
}

// needs mutex
func (d *domainproxyInfo) claimNextAvailableIp6(id string, val net.IP) (ip netip.Addr, ok bool) {
	if preferredAddr, has := d.idMap6[id]; has {
		if preferredAddrVal, has := d.getAddr(preferredAddr); has && preferredAddrVal == nil {
			d.setAddr(preferredAddr, val)
			// id map already has the right value
			logrus.WithFields(logrus.Fields{"id": id, "ip": preferredAddr, "val": val}).Debug("emmie | claimed preferred ip.")
			return preferredAddr, true
		} else {
			logrus.WithFields(logrus.Fields{"id": id, "preferredAddr": preferredAddr, "val": val}).Debug("emmie | could not assign preferred ip.")
		}
	}

	if nextAddr, ok := nextAvailableIp(d.ipMap, d.subnet6, d.lowest6, &d.lastAlloc6, &d.ipsFull6); ok {
		d.setAddr(nextAddr, val)
		d.idMap4[id] = nextAddr

		logrus.WithFields(logrus.Fields{"id": id, "ip": nextAddr, "val": val}).Debug("emmie | claimed available ip.")
		return nextAddr, true
	}

	logrus.WithFields(logrus.Fields{"id": id, "val": val}).Debug("emmie | failed to claim an ip")
	return netip.Addr{}, false
}

// needs mutex
func (d *domainproxyInfo) ensureMachineDomainproxyCorrect(id string, machine *Container) (ip4 net.IP, ip6 net.IP) {
	logrus.WithFields(logrus.Fields{"id": id, "machine": machine}).Debug("emmie | ensure machine domainproxy correct.")

	// prevent us from trying to do stuff with ids that don't make sense
	if id == "" {
		return
	}

	vals, err := machine.GetIPAddrs()
	if err == nil {
		for _, val := range vals {
			if ip4 != nil && val.To4() != nil {
				continue
			}

			if ip6 != nil && val.To4() == nil {
				continue
			}

			var addr netip.Addr
			var is4 bool
			if val.To4() != nil {
				is4 = true
				if mapAddr, has := d.idMap4[id]; has {
					addr = mapAddr
				}
			} else {
				is4 = false
				if mapAddr, has := d.idMap6[id]; has {
					addr = mapAddr
				}
			}

			if addr != (netip.Addr{}) {
				if currentVal, has := d.getAddr(addr); has {
					if is4 {
						ip4 = addr.AsSlice()
					} else {
						ip6 = addr.AsSlice()
					}

					if !currentVal.Equal(val) {
						logrus.WithFields(logrus.Fields{"id": id, "machine": machine, "currentVal": currentVal, "val": val}).Debug("emmie | domainproxy entry wrong.")
						d.setAddr(addr, val)
					} else {
						logrus.WithFields(logrus.Fields{"id": id, "machine": machine, "currentVal": currentVal, "val": val}).Debug("emmie | domainproxy all good")
					}
					continue
				}
			}

			// if we didn't continue, there we didnt have an ip
			logrus.WithFields(logrus.Fields{"id": id, "machine": machine, "val": val}).Debug("emmie | domainproxy has no corresponding ip.")

			if is4 {
				if ip, ok := d.claimNextAvailableIp4(id, val); ok {
					ip4 = ip.AsSlice()
				}
			} else {
				if ip, ok := d.claimNextAvailableIp6(id, val); ok {
					ip6 = ip.AsSlice()
				}
			}
		}
	} else {
		logrus.WithError(err).WithField("name", machine.Name).Debug("failed to get machine IPs for DNS.")
	}

	return
}

func setupDomainProxyInterface(mtu int) error {
	_, domainproxySubnet4, err := net.ParseCIDR(netconf.DomainproxySubnet4Cidr)
	if err != nil {
		return err
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	route := netlink.Route{LinkIndex: lo.Attrs().Index, Dst: domainproxySubnet4, Type: unix.RTN_LOCAL, Scope: unix.RT_SCOPE_HOST, Table: 255}
	err = netlink.RouteAdd(&route)
	if err != nil && errors.Is(err, unix.EEXIST) {
		logrus.Debug("route already exists, readding it")
		err = netlink.RouteDel(&route)
		if err != nil {
			return err
		}
		err = netlink.RouteAdd(&route)
		if err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("error adding route: %w", err)
	}

	//logrus.Debug("creating domainproxy dummy interface and enabling proxy_arp")

	//link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: netconf.DomainproxyDummyName}}
	//err := netlink.LinkAdd(link)
	//if err != nil && errors.Is(err, unix.EEXIST) {
	//	logrus.Debug("domainproxy dummy already exists, recreating")
	//	err = netlink.LinkDel(link)
	//	if err != nil {
	//		return err
	//	}
	//	err = netlink.LinkAdd(link)
	//} else if err != nil {
	//	return fmt.Errorf("error adding link: %v", err)
	//}

	//err = netlink.LinkSetUp(link)
	//if err != nil {
	//	return err
	//}

	// _, domainproxySubnet, err := net.ParseCIDR(netconf.DomainproxySubnet4Cidr)
	// if err != nil {
	// 	return err
	// }

	// route := &netlink.Route{LinkIndex: link.Index, Dst: domainproxySubnet}
	// netlink.RouteAdd(route)
	// if err != nil {
	// 	return err
	// }

	//err = os.WriteFile("/proc/sys/net/ipv4/conf/"+netconf.DomainproxyDummyName+"/proxy_arp", []byte("1"), 0)
	//if err != nil {
	//	return err
	//}

	return nil
}

type mdnsRegistry struct {
	mu syncx.Mutex
	// we store reversed name to do longest prefix match as longest-suffix
	// this allows subdomain wildcards and custom domains to work properly
	tree *radix.Tree

	// this maps container/machine ips to domainproxy ips. the domainproxy ip is what orb.local domains *actually* points to, and lets us do tls interception in an elegant way
	// it makes sense to not have the tree map straight to domainproxy ips because each container has multiple domains, so it would make  juggling the associations more difficult if, say, one domain ended up pointed elsewhere but not the others
	domainproxy domainproxyInfo

	server             *mdns.Server
	cacheFlushDebounce syncx.FuncDebounce
	// must keep track of all queries for cache flushing
	// otherwise we don't know what wildcard queries there could've been
	recentQueries  map[string]mdnsQueryInfo
	pendingFlushes map[string]struct{}

	manager *ConManager
	host    *hclient.Client
	db      *Database

	httpServer *http.Server
}

func newMdnsRegistry(host *hclient.Client, db *Database, manager *ConManager) *mdnsRegistry {
	r := &mdnsRegistry{
		tree: radix.New(),
		domainproxy: newDomainproxyInfo(domainproxySubnet4Prefix,
			// reserve an ip for the error page
			domainproxySubnet4Prefix.Masked().Addr().Next(),
		),
		pendingFlushes: make(map[string]struct{}),
		host:           host,
		db:             db,
		manager:        manager,
	}
	r.cacheFlushDebounce = syncx.NewFuncDebounce(mdnsCacheFlushDebounce, r.flushReusedCache)

	// try to restore recent queries for cross-restart cache invalidation
	// because IPs can shift around if containers are restarted in a diff order
	// and very quickly too, in case user applied update
	// this is a best-effort thing, so don't worry about errors
	recentQueries, err := db.GetDnsRecentQueries()
	if err == nil {
		r.recentQueries = recentQueries
	} else {
		r.recentQueries = make(map[string]mdnsQueryInfo)
		if err != ErrKeyNotFound {
			logrus.WithError(err).Error("failed to restore recent queries")
		}
	}

	// add initial index record
	r.tree.Insert(toTreeKey(mdnsIndexDomain+"."), &mdnsEntry{
		r: r,

		Type:       MdnsEntryStatic,
		IsWildcard: false,
		IsHidden:   true, // don't show itself
		ip4:        net.ParseIP(netconf.SconWebIndexIP4),
		ip6:        net.ParseIP(netconf.SconWebIndexIP6),
	})

	// add k8s alias
	k8sIP4 := net.ParseIP(netconf.SconK8sIP4)
	r.tree.Insert(toTreeKey("k8s.orb.local."), &mdnsEntry{
		r: r,

		Type:       MdnsEntryStatic,
		IsWildcard: true,
		IsHidden:   false,
		ip4:        k8sIP4,
		ip6:        mapToNat64(k8sIP4),
	})

	return r
}

func (r *mdnsRegistry) StartServer(config *mdns.Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var err error
	r.server, err = mdns.NewServer(config)
	if err != nil {
		return err
	}

	tlsController, err := tlsutil.NewTLSController(r.host)
	if err != nil {
		return err
	}

	err = tlsController.LoadRoot()
	if err != nil {
		return err
	}

	// start HTTP index server
	r.httpServer = &http.Server{
		Handler: r,
		TLSConfig: &tls.Config{
			GetCertificate: func(hlo *tls.ClientHelloInfo) (*tls.Certificate, error) {
				// only allow `orb.local` SNI for this server
				if !r.manager.vmConfig.NetworkHttps || hlo.ServerName != mdnsIndexDomain {
					return nil, nil
				}
				return tlsController.MakeCertForHost(hlo.ServerName)
			},
		},
	}
	go runOne("dns index server (http, v4)", func() error {
		l, err := netx.Listen("tcp4", net.JoinHostPort(netconf.SconWebIndexIP4, "80"))
		if err != nil {
			return err
		}
		return r.httpServer.Serve(l)
	})
	go runOne("dns index server (http, v6)", func() error {
		// breaks with DAD on bridge interface
		l, err := netx.Listen("tcp6", net.JoinHostPort(netconf.SconWebIndexIP6, "80"))
		if err != nil {
			return err
		}
		return r.httpServer.Serve(l)
	})
	go runOne("dns index server (https, v4)", func() error {
		l, err := netx.Listen("tcp4", net.JoinHostPort(netconf.SconWebIndexIP4, "443"))
		if err != nil {
			return err
		}
		return r.httpServer.ServeTLS(l, "", "")
	})
	go runOne("dns index server (https, v6)", func() error {
		// breaks with DAD on bridge interface
		l, err := netx.Listen("tcp6", net.JoinHostPort(netconf.SconWebIndexIP6, "443"))
		if err != nil {
			return err
		}
		return r.httpServer.ServeTLS(l, "", "")
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

		name := strings.TrimSuffix(fromTreeKey(s), ".")
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

	proto := "https"
	if req.TLS == nil {
		proto = "http"

		if r.manager.vmConfig.NetworkHttps {
			// attempt to redirect to https:
			// try to import cert
			// if fail, don't redirect
			// if succcess, redirect
			// we ONLY do this for http://orb.local. leave domains alone to avoid breaking curl commands without -L
			err := r.host.ImportTLSCertificate()
			if err != nil {
				logrus.WithError(err).Error("failed to import certificate")
			} else {
				// redirect
				http.Redirect(w, req, "https://"+req.Host+req.URL.Path, http.StatusFound)
				return
			}
		}
	}

	if req.URL.Path != "/" || req.Method != http.MethodGet {
		http.NotFound(w, req)
		return
	}

	// build a list of domains to show
	res := r.listIndexDomains()

	// match request protocol for urls
	res.Proto = proto

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
		if err != nil {
			return err
		}
	}

	// save recent queries
	err := r.db.SetDnsRecentQueries(r.recentQueries)
	if err != nil {
		logrus.WithError(err).Error("failed to save recent queries")
	}

	return nil
}

func (e *mdnsEntry) ensureDnatCorrect() {
	if e.id == "" {
		return
	}

	if e.owningMachine != nil {
		ip4, ip6 := e.r.domainproxy.ensureMachineDomainproxyCorrect(e.id, e.owningMachine)
		e.ip4 = ip4
		e.ip6 = ip6
	}
}

func (e mdnsEntry) ToRecords(qName string, includeV4 bool, includeV6 bool, ttl uint32) []dns.RR {
	var records []dns.RR
	e.ensureDnatCorrect()

	// A
	if includeV4 {
		if e.ip4 != nil {
			// can't combine check bexcause v4 .To16() works
			records = append(records, &dns.A{
				Hdr: dns.RR_Header{
					Name:   qName,
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET | mdnsCacheFlushRrclass,
					Ttl:    ttl,
				},
				A: e.ip4,
			})
		}
	}

	// AAAA
	if includeV6 {
		if e.ip6 != nil {
			records = append(records, &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   qName,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET | mdnsCacheFlushRrclass,
					Ttl:    ttl,
				},
				AAAA: e.ip6,
			})
		} else {
			// if we got none, use NAT64 address derived from IPv4
			// this helps for several reasons:
			//   - Safari (Network.framework) uses interface scoped-address for v4 mDNS response so it can't connect, but it doesn't do scope for v6
			//   - scon machine IPv6 isn't going to conflict with anything, unlike IPv4 and Docker bridges
			//   - we get multi-second delays when returning NSEC for AAAA (due to some unknown changes). returning both is fine
			if e.ip4 != nil {
				records = append(records, &dns.AAAA{
					Hdr: dns.RR_Header{
						Name:   qName,
						Rrtype: dns.TypeAAAA,
						Class:  dns.ClassINET | mdnsCacheFlushRrclass,
						Ttl:    ttl,
					},
					AAAA: mapToNat64(e.ip4),
				})
			}
		}

	}

	return records
}

func reverse(s string) string {
	buf := make([]byte, 0, len(s))
	for i := len(s) - 1; i >= 0; i-- {
		buf = append(buf, s[i])
	}
	return string(buf)
}

func toTreeKey(s string) string {
	// simply reversing the entire thing is fine - as long as we do it consistently
	// also enforce case-insensitivity here, otherwise all-caps machine name breaks if resolved with uppercase chars first
	// mDNSResponder elides cache - it's case insensitive
	return strings.ToLower(reverse(s))
}

func fromTreeKey(s string) string {
	return reverse(s)
}

func mapToNat64(ip4 net.IP) net.IP {
	ip6 := nat64Prefix.Addr().AsSlice()
	copy(ip6[12:], ip4.To4())
	return ip6[:]
}

func (r *mdnsRegistry) containerToMdnsNames(ctr *dockertypes.ContainerSummaryMin, notifyInvalid bool) []dnsName {
	// (3) short ID, names, compose: service.project
	// full ID is too long for DNS: it's 64 chars, max is 63 per component
	names := make([]dnsName, 0, 1+len(ctr.Names)+1)
	// full ID is always hidden
	// all default domains are wildcards, b/c we don't set them up in a hierarchy so they can't conflict
	// TODO: migrate to proper "preferred domain" logic
	names = append(names, dnsName{Name: ctr.ID[:12], Hidden: true, Wildcard: true})
	for _, name := range ctr.Names {
		name = strings.TrimPrefix(name, "/")
		// translate _ to - for RFC compliance, but keep orig $CONTAINER_NAME for convenience, for apps that don't care
		if strings.Contains(name, "_") {
			names = append(names, dnsName{Name: name, Hidden: true, Wildcard: true})
			names = append(names, dnsName{Name: strings.ReplaceAll(name, "_", "-"), Hidden: false, Wildcard: true})
		} else {
			names = append(names, dnsName{Name: name, Hidden: false, Wildcard: true})
		}
	}
	if ctr.Labels != nil {
		if composeProject, ok := ctr.Labels["com.docker.compose.project"]; ok {
			if composeService, ok := ctr.Labels["com.docker.compose.service"]; ok {
				// if we have a compose name, mark all the default ones as hidden
				for i := range names {
					names[i].Hidden = true
				}

				// for --scale: if this is not primary container, append the number
				if composeNum, ok := ctr.Labels["com.docker.compose.container-number"]; ok && composeNum != "1" {
					composeService += "-" + composeNum
				}

				name := composeService + "." + composeProject
				// translate _ to - for RFC compliance, but keep orig $CONTAINER_NAME for convenience, for apps that don't care
				if strings.Contains(name, "_") {
					names = append(names, dnsName{Name: name, Hidden: true, Wildcard: true})
					names = append(names, dnsName{Name: strings.ReplaceAll(name, "_", "-"), Hidden: false, Wildcard: true})
				} else {
					names = append(names, dnsName{Name: name, Hidden: false, Wildcard: true})
				}
			}
		}
	}

	// all names above should have suffixes appended
	suffixedNames := make([]dnsName, 0, len(names))
	for _, name := range names {
		for j, suffix := range mdnsContainerSuffixes {
			// reuse existing array element for first suffix
			suffixedNames = append(suffixedNames, dnsName{
				Name: name.Name + suffix,
				// alias suffixes are always hidden
				Hidden:   name.Hidden || j != 0,
				Wildcard: true,
			})
		}
	}
	names = suffixedNames

	if ctr.Labels != nil {
		if extraNames, ok := ctr.Labels["dev.orbstack.domains"]; ok && extraNames != "" {
			// if we have extra names, mark all the default ones as hidden
			for i := range names {
				names[i].Hidden = true
			}

			for _, nameAndFlags := range strings.Split(extraNames, ",") {
				parts := strings.SplitN(nameAndFlags, ":", 2)
				name := parts[0]

				var flags []string
				if len(parts) > 1 {
					flags = strings.Split(parts[1], ",")
				}

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

				isHidden := slices.Contains(flags, "hidden")
				names = append(names, dnsName{Name: name, Hidden: isHidden, Wildcard: isWildcard})
			}
		}
	}

	return names
}

func mdnsNamesToMdnsId(dnsNames []dnsName) string {
	names := make([]string, len(dnsNames))
	for _, dnsName := range dnsNames {
		names = append(names, dnsName.Name)
	}
	slices.Sort(names)
	return strings.Join(names, ",")
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

func containerToMdnsIPs(ctr *dockertypes.ContainerSummaryMin) []netip.Addr {
	ips := make([]netip.Addr, 0, len(ctr.NetworkSettings.Networks))
	for _, netSettings := range ctr.NetworkSettings.Networks {
		ip4 := netSettings.IPAddress
		if ip, err := netip.ParseAddr(ip4); err == nil {
			ips = append(ips, ip)
		}
		ip6 := netSettings.GlobalIPv6Address
		if ip, err := netip.ParseAddr(ip6); err == nil {
			ips = append(ips, ip)
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
			logrus.WithField("records", flushRecords).Debug("dns: sending cache flush")
		}

		// careful: if any records are invalid, this will fail with rrdata error
		// but it's ok: cache flushing is based on queried records.
		// if a name is invalid (>63 component / >255), it's not possible to query it
		// TODO: for LAN mDNS, call r.host.MdnsSendCacheFlush RPC to send to LAN
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
	mdnsId := mdnsNamesToMdnsId(names)

	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()

	var ip4 net.IP
	var ip6 net.IP
	// we're protected by the mdnsRegistry mutex
	// jank way of getting the first network
	for _, network := range ctr.NetworkSettings.Networks {
		if ip, ok := r.domainproxy.claimNextAvailableIp4(mdnsId, net.ParseIP(network.IPAddress)); ok {
			ip4 = ip.AsSlice()
		}

		if ip, ok := r.domainproxy.claimNextAvailableIp6(mdnsId, net.ParseIP(network.GlobalIPv6Address)); ok {
			ip6 = ip.AsSlice()
		}

		break
	}

	logrus.WithFields(logrus.Fields{
		"names": names,
		"ip4":   ip4,
		"ip6":   ip6,
	}).Debug("dns: add container")

	// we still *add* records if empty IPs (i.e. no netns, like k8s pods) to give them immediate NXDOMAIN in case people do $CONTAINER.orb.local, but hide them to avoid cluttering index
	allHidden := ip4 == nil && ip6 == nil

	for _, name := range names {
		treeKey := toTreeKey(name.Name)
		if _, ok := r.tree.Get(treeKey); ok {
			// we used to allow overriding, but this makes more sense because of cases like https://github.com/orbstack/orbstack/issues/650
			// we could ignore com.docker.compose.oneoff but what if that's not the desired behavior?
			// simply following standard listener/port rules is better
			logrus.WithField("name", name.Name).Warn("dns: name already in use")
			continue
		}

		entry := &mdnsEntry{
			r: r,

			id: mdnsId,

			Type:       MdnsEntryContainer,
			IsWildcard: name.Wildcard,
			// short-ID and aliases are hidden, real names and custom names are not
			IsHidden: allHidden || name.Hidden,

			ip4: ip4,
			ip6: ip6,

			owningDockerCid: ctr.ID,
		}
		r.tree.Insert(treeKey, entry)

		// need to flush any caches? what names were we queried under? (wildcard)
		r.maybeFlushCacheLocked(now, name.Name)
	}
}

func (r *mdnsRegistry) RemoveContainer(ctr *dockertypes.ContainerSummaryMin) {
	names := r.containerToMdnsNames(ctr, false /*notifyInvalid*/)
	mdnsId := mdnsNamesToMdnsId(names)
	logrus.WithField("names", names).Debug("dns: remove container")

	r.mu.Lock()
	defer r.mu.Unlock()

	if ip, has := r.domainproxy.idMap4[mdnsId]; has {
		r.domainproxy.setAddrFreeable(ip)
	}
	if ip, has := r.domainproxy.idMap6[mdnsId]; has {
		r.domainproxy.setAddrFreeable(ip)
	}

	now := time.Now()
	for _, name := range names {
		// don't delete if we're not the owner (e.g. if another container owns it)
		treeKey := toTreeKey(name.Name)
		if oldEntry, ok := r.tree.Get(treeKey); ok {
			entry := oldEntry.(*mdnsEntry)
			if entry.owningDockerCid != ctr.ID {
				logrus.WithField("name", name).Debug("dns: ignoring non-owner delete")
				continue
			}
		}

		r.tree.Delete(treeKey)
		r.maybeFlushCacheLocked(now, name.Name)
	}
}

func (r *mdnsRegistry) AddMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("dns: add machine")

	r.mu.Lock()
	defer r.mu.Unlock()

	// we don't validate these b/c it's not under the user's control
	// TODO allow '_' and translate w/ alias to '-' like Docker
	treeKey := toTreeKey(name)
	if _, ok := r.tree.Get(treeKey); ok {
		// we used to allow overriding, but this makes more sense because of cases like https://github.com/orbstack/orbstack/issues/650
		// we could ignore com.docker.compose.oneoff but what if that's not the desired behavior?
		// simply following standard listener/port rules is better
		logrus.WithField("name", name).Warn("dns: name already in use")
		return
	}

	entry := &mdnsEntry{
		r: r,

		id: name,

		Type:       MdnsEntryMachine,
		IsWildcard: true,
		// machines only have one name, but hide docker
		IsHidden:      c.builtin,
		owningMachine: c,

		ip4: nil,
		ip6: nil,
	}

	entry.ensureDnatCorrect()

	r.tree.Insert(treeKey, entry)

	// need to flush any caches? what names were we queried under? (wildcard)
	r.maybeFlushCacheLocked(time.Now(), name)
}

func (r *mdnsRegistry) RemoveMachine(c *Container) {
	name := c.Name + mdnsMachineSuffix
	logrus.WithField("name", name).Debug("dns: remove machine")

	r.mu.Lock()
	defer r.mu.Unlock()

	if ip, has := r.domainproxy.idMap4[name]; has {
		r.domainproxy.setAddrFreeable(ip)
	}
	if ip, has := r.domainproxy.idMap6[name]; has {
		r.domainproxy.setAddrFreeable(ip)
	}

	// don't delete if we're not the owner (e.g. if docker or another machine owns it)
	treeKey := toTreeKey(name)
	if oldEntry, ok := r.tree.Get(treeKey); ok {
		entry := oldEntry.(*mdnsEntry)
		if entry.owningMachine != c {
			logrus.WithField("name", name).Debug("dns: ignoring non-owner delete")
			return
		}
	}

	r.tree.Delete(treeKey)
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
func nxdomain(q dns.Question, ttl uint32) []dns.RR {
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
			// long ttl is OK for docker because we record it in cache-flush query history,
			// but not for k8s
			Ttl: ttl,
		},
		NextDomain: q.Name,
		TypeBitMap: qTypes,
	}}
}

// dispatcher, to either host proxy or our server
func (r *mdnsRegistry) Records(q dns.Question, from net.Addr) []dns.RR {
	// top bit = "QU" (unicast) flag
	// mDNSResponder sends QU first. not responding causes 1-sec delay
	qclass := q.Qclass &^ mdnsCacheFlushRrclass
	if qclass != dns.ClassINET {
		return nil
	}

	// check src addr:
	// - from macOS: handle as query
	//   * works because swift packet processor redirects and
	//     translates source IPs to known v6, not link local
	// - from a machine: handle as reflector
	fromAddr := from.(*net.UDPAddr)
	if fromAddr.IP.Equal(sconHostBridgeIp4) || fromAddr.IP.Equal(sconHostBridgeIp6) {
		return r.handleQuery(q)
	} else {
		return r.proxyToHost(q)
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
		logrus.WithField("name", q.Name).Debug("dns: lookup")
	}

	// cluster.local is forwarded to k8s kubedns
	if r.manager.k8sEnabled && strings.HasSuffix(q.Name, ".cluster.local.") {
		return r.queryKubeDns(q)
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

func (r *mdnsRegistry) queryKubeDns(q dns.Question) []dns.RR {
	// add .default to .svc.cluster.local
	// we're not querying from a pod, so we don't have a default namespace
	// save original name so we can translate back in received rrs
	origName := q.Name
	names := strings.Split(q.Name, ".")
	// example: traefik, svc, cluster, local, ''
	// example: traefik, default, svc, cluster, local, ''
	if len(names) == 5 && names[1] == "svc" && names[2] == "cluster" && names[3] == "local" && names[4] == "" {
		q.Name = names[0] + ".default.svc.cluster.local."
	}

	k8sMachine, err := r.manager.GetByID(ContainerIDK8s)
	if err != nil {
		logrus.WithError(err).Error("failed to get k8s machine")
		return nil
	}

	// remove QU bit before forwarding query. that makes qclass invalid for unicast DNS
	q.Qclass &^= mdnsCacheFlushRrclass

	// forward to k8s
	// use short TTL (default kubedns = 5 sec) to avoid tracking queries for cache flushing
	rrs, err := UseAgentRet(k8sMachine, func(a *agent.Client) ([]dns.RR, error) {
		rrs, err := a.DockerQueryKubeDns(q)
		if err != nil {
			return nil, err
		}

		// similar to machines code path, successful A + NSEC for AAAA doesn't work, and causes 5-sec delay
		if q.Qtype == dns.TypeAAAA && len(rrs) == 0 {
			// retry as A. if that works, use NAT64
			aRrs, err := a.DockerQueryKubeDns(dns.Question{
				Name:   q.Name,
				Qtype:  dns.TypeA,
				Qclass: q.Qclass,
			})
			if err != nil {
				// if fallback A query failed, return none
				return nil, nil
			}

			// if fallback A query succeeded, use NAT64 to create AAAA
			for _, rr := range aRrs {
				if a, ok := rr.(*dns.A); ok {
					rrs = append(rrs, &dns.AAAA{
						Hdr: dns.RR_Header{
							Name:   q.Name,
							Rrtype: dns.TypeAAAA,
							Class:  dns.ClassINET | mdnsCacheFlushRrclass,
							Ttl:    kubeDnsTTLSeconds,
						},
						AAAA: mapToNat64(a.A),
					})
				}
			}
		}

		return rrs, nil
	})
	if err != nil {
		logrus.WithError(err).Error("failed to query kubedns")
		return nil
	}

	// 0 rrs = nxdomain (NSEC)
	if len(rrs) == 0 {
		return nxdomain(q, kubeDnsTTLSeconds)
	}

	// set cache flush on all records, and translate names back (if we changed to default ns)
	for _, rr := range rrs {
		rr.Header().Class |= mdnsCacheFlushRrclass
		if rr.Header().Name == q.Name {
			rr.Header().Name = origName
		}
	}

	return rrs
}

// the idea: we return NSEC if not found AND we know we're in control of the name
// that means we either got a tree match but rejected it, or it's under our suffix
func (r *mdnsRegistry) getRecordsLocked(q dns.Question, includeV4 bool, includeV6 bool) []dns.RR {
	treeKey := toTreeKey(q.Name)
	matchedKey, _entry, ok := r.tree.LongestPrefix(treeKey)
	if verboseDebug {
		logrus.WithFields(logrus.Fields{
			"treeKey":    treeKey,
			"matchedKey": matchedKey,
			"entry":      _entry,
			"ok":         ok,
		}).Debug("dns: lookup result")
	}
	if !ok {
		// no match at all.
		// return NSEC only if it's under our main suffix
		// otherwise we can't take responsibility for this name
		if strings.HasSuffix(q.Name, mdnsContainerSuffixes[0]) {
			return nxdomain(q, mdnsTTLSeconds)
		} else {
			return nil
		}
	}
	entry := _entry.(*mdnsEntry)

	// if not an exact match: is wildcard allowed?
	if matchedKey != treeKey {
		// this was a wildcard match. is that allowed?
		// allow any number of wildcard components (*.*)
		if !entry.IsWildcard {
			return nxdomain(q, mdnsTTLSeconds)
		}

		// make sure we're matching on a component boundary:
		// check that the next character is a dot
		// e.g. stack.local shouldn't match orbstack.local
		if len(treeKey) > len(matchedKey) && treeKey[len(matchedKey)] != '.' {
			return nxdomain(q, mdnsTTLSeconds)
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
		return nxdomain(q, mdnsTTLSeconds)
	}

	return records
}

func (r *mdnsRegistry) proxyToHost(q dns.Question) []dns.RR {
	if verboseDebug {
		logrus.WithField("name", q.Name).Debug("dns: proxy to host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mdnsProxyTimeout)
	defer cancel()

	// remove QU bit before forwarding query. that makes qclass invalid for unicast DNS
	// even though end goal is mDNS, we still need to send a valid qclass to mDNSResponder
	q.Qclass &^= mdnsCacheFlushRrclass

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
