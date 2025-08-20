package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util/dirfs"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/util/netutil"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	invalidateHostProbeDebounceDuration = 100 * time.Millisecond
)

var (
	errNoMoreIPs = errors.New("no more ips")
)

type domainproxyAllocator struct {
	hostMap map[domainproxytypes.Host]netip.Addr

	nameMap map[string]netip.Addr

	ipsFull   bool
	subnet    netip.Prefix
	lowest    netip.Addr
	lastAlloc netip.Addr
}

func newDomainproxyAllocator(subnet netip.Prefix, lowest netip.Addr) *domainproxyAllocator {
	return &domainproxyAllocator{
		hostMap: make(map[domainproxytypes.Host]netip.Addr),

		nameMap: make(map[string]netip.Addr),

		ipsFull:   false,
		subnet:    subnet,
		lowest:    lowest,
		lastAlloc: lowest,
	}
}

type domainproxyHostState struct {
	// not owned!
	procDirfs                   *dirfs.FS
	hasNetnsCookie              bool
	netnsCookie                 uint64
	invalidateHostProbeDebounce *syncx.LeadingFuncDebounce
}

type domainproxyRegistry struct {
	manager         *ConManager
	dockerMachine   *Container
	bridgeLinkIndex int

	mu syncx.Mutex

	// maps domainproxy ips to container ips. we call container ips values
	ipMap map[netip.Addr]domainproxytypes.Upstream

	// maps domain names to domainproxy ips
	v4 *domainproxyAllocator
	v6 *domainproxyAllocator

	tproxy *bpf.Tproxy

	domainTLSProxy       *domainproxy.DomainTLSProxy
	domainTLSProxyActive bool

	domainSSHProxy *domainproxy.DomainSSHProxy

	hostState          map[domainproxytypes.Host]*domainproxyHostState
	netnsCookieToHosts map[uint64][]domainproxytypes.Host
}

func newDomainproxyRegistry(mdnsRegistry *mdnsRegistry, subnet4 netip.Prefix, lowest4 netip.Addr, subnet6 netip.Prefix, lowest6 netip.Addr) (*domainproxyRegistry, error) {
	tproxy, err := bpf.NewTproxy(subnet4, subnet6, []uint16{443, 22})
	if err != nil {
		return nil, fmt.Errorf("new tproxy: %w", err)
	}

	d := &domainproxyRegistry{
		manager:         mdnsRegistry.manager,
		dockerMachine:   nil,
		bridgeLinkIndex: -1,

		ipMap: make(map[netip.Addr]domainproxytypes.Upstream),

		v4: newDomainproxyAllocator(subnet4, lowest4),
		v6: newDomainproxyAllocator(subnet6, lowest6),

		tproxy: tproxy,

		domainTLSProxyActive: false,
		hostState:            make(map[domainproxytypes.Host]*domainproxyHostState),
		netnsCookieToHosts:   make(map[uint64][]domainproxytypes.Host),
	}

	cb := &SconProxyCallbacks{mdnsRegistry: mdnsRegistry, dpRegistry: d}
	tlsProxy, err := domainproxy.NewDomainTLSProxy(mdnsRegistry.host, cb)
	if err != nil {
		return nil, fmt.Errorf("new tls domainproxy: %w", err)
	}
	d.domainTLSProxy = tlsProxy

	d.domainSSHProxy = domainproxy.NewDomainSSHProxy(cb)

	return d, nil
}

func (d *domainproxyRegistry) StartTLSProxy() error {
	return d.domainTLSProxy.Start(netconf.VnetTproxyIP4, netconf.VnetTproxyIP6, d.manager.net.netconf.DomainproxySubnet4, d.manager.net.netconf.DomainproxySubnet6, netconf.QueueDomainproxyHttpProbe, d.tproxy)
}

func (d *domainproxyRegistry) StartSSHProxy(handler domainproxy.SSHHandler) error {
	return d.domainSSHProxy.Start(d.tproxy, handler)
}

func (d *domainproxyRegistry) addNeighbor(ip netip.Addr) {
	// note that we never remove neighbors. this is okay, and in fact better for performance because this function only ever gets called with domainproxy ipv6s that are already anyip
	// this is just a work around because anyip ipv6 doesn't do ndp advertisements, unlike ipv4 which does do arp
	if d.bridgeLinkIndex < 0 {
		bridge, err := netlink.LinkByName(ifBridge)
		if err != nil {
			logrus.Debug("unable to get bridge link: %w", err)
			return
		}
		d.bridgeLinkIndex = bridge.Attrs().Index
	}

	var err error
	if ip.Is6() {
		err = netlink.NeighAdd(
			&netlink.Neigh{
				Family:    unix.AF_INET6,
				Flags:     netlink.NTF_PROXY,
				State:     netlink.NUD_PERMANENT,
				Type:      unix.RTN_UNSPEC,
				LinkIndex: d.bridgeLinkIndex,
				IP:        ip.AsSlice(),
			},
		)
	}
	if err != nil && !errors.Is(err, unix.EEXIST) {
		logrus.WithError(err).Error("failed to add neighbor")
	}
}

func (d *domainproxyRegistry) invalidateAddrProbeLocked(ip netip.Addr) {
	logrus.WithField("ip", ip).Debug("invalidating addr probe")

	prefix := "domainproxy4"
	if ip.Is6() {
		prefix = "domainproxy6"
	}

	err := nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
		// may not exist if never probed successfully
		err := nft.SetDeleteByName(conn, table, prefix+"_probed_tls", nft.IPAddr(ip))
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy 6")
		}

		// also may not exist if never probed successfully
		err = nft.MapDeleteByName(conn, table, prefix+"_probed_http_upstreams", nft.IPAddr(ip))
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy 7")
		}

		err = nft.SetDeleteByName(conn, table, prefix+"_probed_ssh_upstream", nft.IPAddr(ip))
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy 9")
		}

		err = nft.SetDeleteByName(conn, table, prefix+"_probed_ssh_no_upstream", nft.IPAddr(ip))
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy 10")
		}

		return nil
	})
	if err != nil {
		logrus.WithError(err).Error("failed to remove from domainproxy 8")
	}

	// always try to invalidate in docker
	if d.dockerMachine == nil {
		return
	}

	_, err = withContainerNetns(d.dockerMachine, func() (struct{}, error) {
		return struct{}{}, nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
			// may not exist if never probed successfully
			err = nft.SetDeleteByName(conn, table, prefix+"_probed_tls", nft.IPAddr(ip))
			if err != nil && !errors.Is(err, unix.ENOENT) {
				return err
			}

			// may not exist if never probed successfully
			err = nft.MapDeleteByName(conn, table, prefix+"_probed_http_upstreams", nft.IPAddr(ip))
			if err != nil && !errors.Is(err, unix.ENOENT) {
				return err
			}

			return nil
		})
	})
	if err != nil && !errors.Is(err, ErrMachineNotRunning) {
		logrus.WithError(err).Error("failed to delete from docker domainproxy")
	}
}

func (d *domainproxyRegistry) freeAddrLocked(ip netip.Addr) {
	upstream, ok := d.ipMap[ip]
	if !ok || !upstream.IsValid() {
		return
	}

	d.ipMap[ip] = domainproxytypes.Upstream{}

	if ip.Is4() {
		if hostMapIP, ok := d.v4.hostMap[upstream.Host]; ok && hostMapIP == ip {
			delete(d.v4.hostMap, upstream.Host)
		}
	} else {
		if hostMapIP, ok := d.v6.hostMap[upstream.Host]; ok && hostMapIP == ip {
			delete(d.v6.hostMap, upstream.Host)
		}
	}

	prefix := "domainproxy4"
	if ip.Is6() {
		prefix = "domainproxy6"
	}

	if upstream.Host.Type == domainproxytypes.HostTypeDocker {
		if d.dockerMachine != nil {
			_, err := withContainerNetns(d.dockerMachine, func() (struct{}, error) {
				return struct{}{}, nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
					err := nft.MapDeleteByName(conn, table, prefix, nft.IPAddr(ip))
					if err != nil {
						return err
					}

					err = nft.SetDeleteByName(conn, table, prefix+"_masquerade", nft.Concat(nft.IP(upstream.IP), nft.IP(upstream.IP)))
					if err != nil {
						return err
					}
					return nil
				})
			})
			// this will happen if docker is not running -- very possible if the docker machine just shut down
			if err != nil && !errors.Is(err, ErrMachineNotRunning) {
				logrus.WithError(err).Error("failed to delete from docker domainproxy")
			}
		}

		err := nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
			return nft.SetDeleteByName(conn, table, prefix+"_docker", nft.IPAddr(ip))
		})
		if err != nil {
			logrus.WithError(err).Error("failed to delete from domainproxy 1")
		}
	}

	err := nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
		err := nft.MapDeleteByName(conn, table, prefix, nft.IPAddr(ip))
		if err != nil {
			logrus.WithError(err).Error("failed to remove from domainproxy 2")
		}

		err = nft.SetDeleteByName(conn, table, prefix+"_masquerade", nft.Concat(nft.IPAddr(ip), nft.IP(upstream.IP)))
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy 3")
		}

		return nil
	})
	if err != nil {
		logrus.WithError(err).Error("failed to remove from domainproxy 4")
	}

	err = nft.WithTable(nft.FamilyBridge, netconf.NftableBridge, func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetDeleteByName(conn, table, prefix+"_masquerade_bridge", nft.Concat(nft.IPAddr(ip), nft.IP(upstream.IP)))
	})
	if err != nil {
		logrus.WithError(err).Error("failed to remove from domainproxy 5")
	}

	d.invalidateAddrProbeLocked(ip)
}

func (d *domainproxyRegistry) setAddrUpstreamLocked(ip netip.Addr, val domainproxytypes.Upstream) {
	if !val.IsValid() {
		d.freeAddrLocked(ip)
		return
	}

	currVal, ok := d.ipMap[ip]
	if ok {
		if currVal.ValEqual(val) {
			// we don't need to make any changes
			return
		} else {
			// make sure the element gets removed before we change it to something else
			d.freeAddrLocked(ip)
		}
	}

	if ip.Is4() {
		if hostMapIP, ok := d.v4.hostMap[val.Host]; ok && hostMapIP != ip {
			logrus.WithField("host", val.Host).Debug("overwriting hostmap ip")
			d.freeAddrLocked(hostMapIP)
		}
		d.v4.hostMap[val.Host] = ip
	} else {
		if hostMapIP, ok := d.v6.hostMap[val.Host]; ok && hostMapIP != ip {
			logrus.WithField("host", val.Host).Debug("overwriting hostmap ip")
			d.freeAddrLocked(hostMapIP)
		}
		d.v6.hostMap[val.Host] = ip
	}

	prefix := "domainproxy4"
	if ip.Is6() {
		prefix = "domainproxy6"
	}

	if val.Host.Type == domainproxytypes.HostTypeDocker {
		if d.dockerMachine != nil {
			_, err := withContainerNetns(d.dockerMachine, func() (struct{}, error) {
				return struct{}{}, nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
					err := nft.MapAddByName(conn, table, prefix, nft.IPAddr(ip), nft.IP(val.IP))
					if err != nil {
						return err
					}

					// in docker it's val.IP -> val.IP because it's post-dnat
					err = nft.SetAddByName(conn, table, prefix+"_masquerade", nft.Concat(nft.IP(val.IP), nft.IP(val.IP)))
					if err != nil {
						return err
					}

					return nil
				})
			})
			if err != nil {
				logrus.WithError(err).Error("failed to add to docker domainproxy")
			}
		}

		err := nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
			return nft.SetAddByName(conn, table, prefix+"_docker", nft.IPAddr(ip))
		})
		if err != nil {
			// obfuscate errors
			logrus.WithError(err).Error("failed to add to domainproxy 1")
		}
	}

	d.addNeighbor(ip)

	err := nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
		err := nft.MapAddByName(conn, table, prefix, nft.IPAddr(ip), nft.IP(val.IP))
		if err != nil {
			logrus.WithError(err).Error("failed to add to domainproxy 2")
		}

		// in machines it's ip -> val.IP because it's pre-dnat
		err = nft.SetAddByName(conn, table, prefix+"_masquerade", nft.Concat(nft.IPAddr(ip), nft.IP(val.IP)))
		if err != nil {
			logrus.WithError(err).Error("failed to add to domainproxy 3")
		}

		return nil
	})
	if err != nil {
		logrus.WithError(err).Error("failed to add to domainproxy 4")
	}

	err = nft.WithTable(nft.FamilyBridge, netconf.NftableBridge, func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetAddByName(conn, table, prefix+"_masquerade_bridge", nft.Concat(nft.IPAddr(ip), nft.IP(val.IP)))
	})
	if err != nil {
		logrus.WithError(err).Error("failed to add to domainproxy 5")
	}

	d.ipMap[ip] = val
}

func (d *domainproxyRegistry) nextAvailableIPLocked(allocator *domainproxyAllocator) (ip netip.Addr, ok bool) {
	ip = allocator.lastAlloc

	var reclaimableIP netip.Addr
	foundReclaimableIP := false

	for {
		ip = ip.Next()

		// wrap around
		if !allocator.subnet.Contains(ip) {
			ip = allocator.lowest
		}

		val, ok := d.ipMap[ip]
		if !ok {
			allocator.lastAlloc = ip
			return ip, true
		}

		// freeable ips are zero ips. they can be reclaimed but we're hoping to reuse them for the domain they were used for originally
		if !foundReclaimableIP && !val.IsValid() {
			reclaimableIP = ip
			foundReclaimableIP = true
			// we already know that we're not gonna find any free spots. take our freeable and run!
			if allocator.ipsFull {
				break
			}
		}

		// we wrapped all the way around, we're out of ips
		if ip == allocator.lastAlloc {
			allocator.ipsFull = true
			break
		}
	}

	if foundReclaimableIP {
		allocator.lastAlloc = reclaimableIP
		return reclaimableIP, true
	} else {
		return netip.Addr{}, false
	}
}

func (d *domainproxyRegistry) findReclaimableAddrLocked(allocator *domainproxyAllocator, names []string) (addr netip.Addr, ok bool) {
	// try to find something we can claim
	for _, name := range names {
		addr, ok := allocator.nameMap[name]
		if !ok {
			continue
		}

		upstream, ok := d.ipMap[addr]
		if !ok || upstream.IsValid() {
			// we only want reclaimable ips, where there's an invalid upstream in the ipmap
			continue
		}

		return addr, true
	}

	return netip.Addr{}, false
}

func (d *domainproxyRegistry) assignUpstreamLocked(allocator *domainproxyAllocator, val domainproxytypes.Upstream) (addr netip.Addr, err error) {
	needsClaim := false
	addr, ok := allocator.hostMap[val.Host]
	if !ok {
		needsClaim = true
		var reclaimableAddr netip.Addr
		reclaimableAddr, ok = d.findReclaimableAddrLocked(allocator, val.Names)
		if ok {
			addr = reclaimableAddr
		} else {
			// couldn't find a reclaimable ip, allocate
			addr, ok = d.nextAvailableIPLocked(allocator)
			if !ok {
				return netip.Addr{}, errNoMoreIPs
			}
		}
	}

	if needsClaim {
		for _, name := range val.Names {
			allocator.nameMap[name] = addr
		}
	}

	d.setAddrUpstreamLocked(addr, val)
	return addr, nil
}

func (d *domainproxyRegistry) AssignUpstream(allocator *domainproxyAllocator, val domainproxytypes.Upstream) (addr netip.Addr, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.assignUpstreamLocked(allocator, val)
}

func (d *domainproxyRegistry) updateHostStateFromProcDirfsLocked(host domainproxytypes.Host, dirfs *dirfs.FS) error {
	hostState, ok := d.hostState[host]
	if !ok {
		return errors.New("host state not found")
	}

	hostState.procDirfs = dirfs

	netnsCookie, err := sysnet.WithNetnsProcDirfs(dirfs, func() (uint64, error) {
		return sysnet.GetNetnsCookie()
	})
	if err != nil {
		return fmt.Errorf("get netns cookie: %w", err)
	}
	hostState.netnsCookie = netnsCookie
	hostState.hasNetnsCookie = true

	hostState.invalidateHostProbeDebounce = syncx.NewLeadingFuncDebounce(invalidateHostProbeDebounceDuration, func() {
		d.invalidateHostProbe(host)
	})

	d.netnsCookieToHosts[netnsCookie] = append(d.netnsCookieToHosts[netnsCookie], host)

	err = d.manager.net.portMonitor.RegisterNetnsInterest("mdns_domainproxy", netnsCookie)
	if err != nil {
		return err
	}

	return nil
}

func (d *domainproxyRegistry) freeHostLocked(host domainproxytypes.Host) {
	if ip, ok := d.v4.hostMap[host]; ok {
		d.freeAddrLocked(ip)
	}
	if ip, ok := d.v6.hostMap[host]; ok {
		d.freeAddrLocked(ip)
	}
	if hostState, ok := d.hostState[host]; ok {
		if hostState.hasNetnsCookie {
			d.manager.net.portMonitor.DeregisterNetnsInterest("mdns_domainproxy", hostState.netnsCookie)

			d.netnsCookieToHosts[hostState.netnsCookie] = slices.DeleteFunc(d.netnsCookieToHosts[hostState.netnsCookie], func(h domainproxytypes.Host) bool {
				return h == host
			})
			if len(d.netnsCookieToHosts[hostState.netnsCookie]) == 0 {
				delete(d.netnsCookieToHosts, hostState.netnsCookie)
			}
		}

		delete(d.hostState, host)
	}
}

func (d *domainproxyRegistry) FreeHost(host domainproxytypes.Host) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.freeHostLocked(host)
}

func containerToMdnsIPs(ctr *dockertypes.ContainerSummaryMin) (net.IP, net.IP) {
	var ip4 net.IP
	var ip6 net.IP
	for _, netSettings := range ctr.NetworkSettings.Networks {
		if ip4 == nil {
			ip4 = net.ParseIP(netSettings.IPAddress)
		}

		if ip6 == nil {
			ip6 = net.ParseIP(netSettings.GlobalIPv6Address)
		}

		if ip4 != nil && ip6 != nil {
			break
		}
	}

	return ip4, ip6
}

func (d *domainproxyRegistry) AddContainer(ctr *dockertypes.ContainerSummaryMin, dirfs *dirfs.FS, nameStrings []string) (net.IP, net.IP) {
	d.mu.Lock()
	defer d.mu.Unlock()

	domainproxyHost := domainproxytypes.Host{Type: domainproxytypes.HostTypeDocker, ID: ctr.ID}

	hostState := &domainproxyHostState{}
	d.hostState[domainproxyHost] = hostState

	if dirfs != nil {
		err := d.updateHostStateFromProcDirfsLocked(domainproxyHost, dirfs)
		if err != nil {
			logrus.WithError(err).Error("failed to set host state while adding docker container to domainproxy")
		}
	}

	var httpPortOverride uint16
	var httpsPortOverride uint16
	if port, ok := ctr.Labels["dev.orbstack.https-port"]; ok {
		if portInt, err := strconv.ParseUint(port, 10, 16); err == nil {
			logrus.WithField("port", portInt).Debug("setting https port override")
			httpsPortOverride = uint16(portInt)
		} else {
			logrus.WithError(err).WithField("port", port).Error("failed to parse https port override")
		}
	}
	if port, ok := ctr.Labels["dev.orbstack.http-port"]; ok {
		if portInt, err := strconv.ParseUint(port, 10, 16); err == nil {
			logrus.WithField("port", portInt).Debug("setting http port override")
			httpPortOverride = uint16(portInt)
		} else {
			logrus.WithError(err).WithField("port", port).Error("failed to parse http port override")
		}
	}

	ctrIP4, ctrIP6 := containerToMdnsIPs(ctr)
	var ip4 net.IP
	var ip6 net.IP
	// we're protected by the mdnsRegistry mutex
	if ctrIP4 != nil {
		ip, err := d.assignUpstreamLocked(d.v4, domainproxytypes.Upstream{
			Host:  domainproxyHost,
			Names: nameStrings,
			IP:    ctrIP4,

			HTTPPortOverride:  httpPortOverride,
			HTTPSPortOverride: httpsPortOverride,
		})
		if err != nil {
			logrus.WithError(err).WithField("cid", ctr.ID).Debug("failed to assign ip4 for DNS")
		} else {
			ip4 = ip.AsSlice()
		}
	}
	if ctrIP6 != nil {
		ip, err := d.assignUpstreamLocked(d.v6, domainproxytypes.Upstream{
			Host:  domainproxyHost,
			Names: nameStrings,
			IP:    ctrIP6,

			HTTPPortOverride:  httpPortOverride,
			HTTPSPortOverride: httpsPortOverride,
		})
		if err != nil {
			logrus.WithError(err).WithField("cid", ctr.ID).Debug("failed to assign ip6 for DNS")
		} else {
			ip6 = ip.AsSlice()
		}
	}

	return ip4, ip6
}

func (d *domainproxyRegistry) RemoveContainer(ctr *dockertypes.ContainerSummaryMin) {
	d.mu.Lock()
	defer d.mu.Unlock()

	domainproxyHost := domainproxytypes.Host{Type: domainproxytypes.HostTypeDocker, ID: ctr.ID}

	d.freeHostLocked(domainproxyHost)
}

func (d *domainproxyRegistry) AddMachine(c *Container) {
	d.mu.Lock()
	defer d.mu.Unlock()

	domainproxyHost := domainproxytypes.Host{Type: domainproxytypes.HostTypeMachine, ID: c.ID}

	d.hostState[domainproxyHost] = &domainproxyHostState{}

	rt, err := c.RuntimeState()
	if err != nil {
		logrus.WithError(err).WithField("container", c.ID).Error("failed to get runtime state")
		return
	}

	err = d.updateHostStateFromProcDirfsLocked(domainproxyHost, rt.InitProcDirfd)
	if err != nil {
		logrus.WithError(err).WithField("container", c.ID).Error("failed to update host state")
	}

	if c.ID == ContainerIDK8s {
		k8sHost := domainproxytypes.Host{Type: domainproxytypes.HostTypeK8s, ID: ContainerIDK8s}
		d.hostState[k8sHost] = &domainproxyHostState{}

		err = d.updateHostStateFromProcDirfsLocked(k8sHost, rt.InitProcDirfd)
		if err != nil {
			logrus.WithError(err).WithField("container", c.ID).Error("failed to update host state for k8s")
		}
	}
}

func (d *domainproxyRegistry) RemoveMachine(c *Container) {
	d.mu.Lock()
	defer d.mu.Unlock()

	domainproxyHost := domainproxytypes.Host{Type: domainproxytypes.HostTypeMachine, ID: c.ID}

	d.freeHostLocked(domainproxyHost)

	if c.ID == ContainerIDK8s {
		k8sHost := domainproxytypes.Host{Type: domainproxytypes.HostTypeK8s, ID: ContainerIDK8s}
		d.freeHostLocked(k8sHost)
	}
}

func (d *domainproxyRegistry) EnsureMachineIPsCorrect(names []string, machine *Container) (net.IP, net.IP) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var ip4 net.IP
	var ip6 net.IP

	valips, err := machine.GetIPAddrs()
	if err != nil {
		logrus.WithError(err).WithField("name", machine.Name).Debug("failed to get machine IPs for DNS")
		return nil, nil
	}

	for _, valip := range valips {
		if ip4 == nil && valip.Is4() {
			addr, err := d.assignUpstreamLocked(d.v4, domainproxytypes.Upstream{
				Host:  domainproxytypes.Host{Type: domainproxytypes.HostTypeMachine, ID: machine.ID},
				Names: names,
				IP:    net.IP(valip.AsSlice()),
			})
			if err != nil {
				logrus.WithError(err).WithField("name", machine.Name).Debug("failed to assign ip4 for DNS")
				continue
			}

			ip4 = addr.AsSlice()
		}

		if ip6 == nil && valip.Is6() {
			addr, err := d.assignUpstreamLocked(d.v6, domainproxytypes.Upstream{
				Host:  domainproxytypes.Host{Type: domainproxytypes.HostTypeMachine, ID: machine.ID},
				Names: names,
				IP:    net.IP(valip.AsSlice()),
			})
			if err != nil {
				logrus.WithError(err).WithField("name", machine.Name).Debug("failed to assign ip6 for DNS")
				continue
			}

			ip6 = addr.AsSlice()
		}
	}

	return ip4, ip6
}

func (d *domainproxyRegistry) invalidateHostProbe(host domainproxytypes.Host) {
	d.mu.Lock()
	defer d.mu.Unlock()

	logrus.WithFields(logrus.Fields{
		"host": host,
	}).Debug("invalidating host probe")

	if ip, ok := d.v4.hostMap[host]; ok {
		d.invalidateAddrProbeLocked(ip)
	} else {
		logrus.WithField("host", host).Debug("no ipv4 found for host to invalidate")
	}
	if ip, ok := d.v6.hostMap[host]; ok {
		d.invalidateAddrProbeLocked(ip)
	} else {
		logrus.WithField("host", host).Debug("no ipv6 found for host to invalidate")
	}
}

func (d *domainproxyRegistry) RefreshHostListeners(ev bpf.PortMonitorEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, host := range d.netnsCookieToHosts[ev.NetnsCookie] {
		if hostState, ok := d.hostState[host]; ok && hostState.invalidateHostProbeDebounce != nil {
			// this won't deadlock because debounce calls in a goroutine
			hostState.invalidateHostProbeDebounce.Call()
		}
	}

}

func (d *domainproxyRegistry) updateTLSProxyNftables(enabled bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var err error
	if !d.domainTLSProxyActive && enabled {
		// we need to activate it
		// TODO: migrate to nft library
		err = nft.Run("add", "rule", "inet", netconf.NftableInet, "prerouting-dynamic-tlsproxy", "jump prerouting-tlsproxy")
	} else if d.domainTLSProxyActive && !enabled {
		// we need to deactivate it
		err = nft.FlushChain(nft.FamilyInet, netconf.NftableInet, "prerouting-dynamic-tlsproxy")
	}
	if err != nil {
		return err
	}

	d.domainTLSProxyActive = enabled
	return nil
}

func setupDomainProxyInterface(netconf *netconf.Config) error {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	// this is an anyip route, which causes linux to treat the entire domainproxy subnet as its own ips
	route4 := netlink.Route{LinkIndex: lo.Attrs().Index, Dst: netutil.PrefixToIPNet(netconf.DomainproxySubnet4), Type: unix.RTN_LOCAL, Scope: unix.RT_SCOPE_HOST, Table: 255}
	err = netlink.RouteAdd(&route4)
	if err != nil && errors.Is(err, unix.EEXIST) {
		logrus.Debug("route4 already exists, readding it")
		err = netlink.RouteDel(&route4)
		if err != nil {
			return err
		}
		err = netlink.RouteAdd(&route4)
		if err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("add route: %w", err)
	}

	// also an anyip route
	route6 := netlink.Route{LinkIndex: lo.Attrs().Index, Dst: netutil.PrefixToIPNet(netconf.DomainproxySubnet6), Type: unix.RTN_LOCAL, Scope: unix.RT_SCOPE_HOST, Table: 255}
	err = netlink.RouteAdd(&route6)
	if err != nil && errors.Is(err, unix.EEXIST) {
		logrus.Debug("route6 already exists, readding it")
		err = netlink.RouteDel(&route6)
		if err != nil {
			return err
		}
		err = netlink.RouteAdd(&route6)
		if err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("add route: %w", err)
	}

	err = os.WriteFile("/proc/sys/net/ipv6/conf/"+ifBridge+"/proxy_ndp", []byte("1"), 0)
	if err != nil {
		return fmt.Errorf("enable proxy ndp: %w", err)
	}

	return nil
}

type SconProxyCallbacks struct {
	mdnsRegistry *mdnsRegistry
	dpRegistry   *domainproxyRegistry
}

func (c *SconProxyCallbacks) GetUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error) {
	return c.mdnsRegistry.getProxyUpstreamByHost(host, v4)
}

func (c *SconProxyCallbacks) GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	return c.dpRegistry.getProxyUpstreamByAddr(addr)
}

func (c *SconProxyCallbacks) GetMark(upstream domainproxytypes.Upstream) int {
	mark := netconf.VmFwmarkTproxyOutboundBit
	if upstream.Host.Type == domainproxytypes.HostTypeDocker {
		mark |= netconf.VmFwmarkDockerRouteBit
	}

	return mark
}

func (c *SconProxyCallbacks) NfqueueMarkSkip(mark uint32) uint32 {
	return mark | netconf.VmFwmarkNfqueueSkipBit
}

func (c *SconProxyCallbacks) NftableName() string {
	return netconf.NftableInet
}

func (c *SconProxyCallbacks) GetHostOpenPorts(host domainproxytypes.Host) (map[uint16]struct{}, error) {
	return c.dpRegistry.getHostOpenPorts(host)
}

func (r *mdnsRegistry) getProxyUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var proxyAddr netip.Addr
	if proxyAddrVal, err := netip.ParseAddr(host); err == nil {
		proxyAddr = proxyAddrVal
	} else {
		proxyIP4, proxyIP6 := r.getIPsForNameLocked(strings.TrimSuffix(host, ".") + ".")

		if v4 && proxyIP4 != nil {
			if proxyAddr4, ok := netip.AddrFromSlice(proxyIP4); ok {
				proxyAddr = proxyAddr4
			}
		}
		if !v4 && proxyIP6 != nil {
			if proxyAddr6, ok := netip.AddrFromSlice(proxyIP6); ok {
				proxyAddr = proxyAddr6
			}
		}
	}

	if !proxyAddr.IsValid() {
		return netip.Addr{}, domainproxytypes.Upstream{}, errors.New("could not find proxyaddr")
	}

	upstream, err := r.domainproxy.getProxyUpstreamByAddr(proxyAddr)
	if err != nil {
		return proxyAddr, domainproxytypes.Upstream{}, err
	}

	return proxyAddr, upstream, nil
}

func (d *domainproxyRegistry) getProxyUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	upstream, ok := d.ipMap[addr]
	if !ok {
		return domainproxytypes.Upstream{}, errors.New("could not find backend in mdns registry")
	}

	return upstream, nil
}

func (d *domainproxyRegistry) getHostOpenPorts(domainproxyHost domainproxytypes.Host) (map[uint16]struct{}, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if domainproxyHost.ID == "" {
		return nil, errors.New("no host id")
	}

	openPorts := map[uint16]struct{}{}

	if domainproxyHost.Type == domainproxytypes.HostTypeMachine || domainproxyHost.Type == domainproxytypes.HostTypeK8s {
		// grab nftables ports
		machine, err := d.manager.GetByID(domainproxyHost.ID)
		if err != nil {
			return nil, err
		}

		_, err = withContainerNetns(machine, func() (struct{}, error) {
			return struct{}{}, sysnet.GetNftablesPorts(openPorts)
		})
		if err != nil {
			return nil, err
		}
	}

	hostState, ok := d.hostState[domainproxyHost]
	if !ok || hostState.procDirfs == nil {
		return nil, fmt.Errorf("no proc dirfd for host: %v", domainproxyHost)
	}

	// always grab both v4 and v6 ports because dual stack shows up as ipv6 anyways, so not worth the effort to differentiate
	// especially when our probing routine should be relatively fast anyways, especially for non-listening ports
	listeners4, err := sysnet.ReadProcNetFromDirfs(hostState.procDirfs, "tcp")
	if err != nil {
		return nil, err
	}
	listeners6, err := sysnet.ReadProcNetFromDirfs(hostState.procDirfs, "tcp6")
	if err != nil {
		return nil, err
	}

	if domainproxyHost.Type == domainproxytypes.HostTypeK8s {
		// for k8s, we filter out docker ports, which will have a proc net entry
		for _, listener := range listeners4 {
			delete(openPorts, listener.Port())
		}
		for _, listener := range listeners6 {
			delete(openPorts, listener.Port())
		}
	} else {
		for _, listener := range listeners4 {
			openPorts[listener.Port()] = struct{}{}
		}
		for _, listener := range listeners6 {
			openPorts[listener.Port()] = struct{}{}
		}
	}

	return openPorts, nil
}
