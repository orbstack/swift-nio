package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/google/nftables"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var (
	errNoMoreIPs = errors.New("no more ips")
)

type domainproxyAllocator struct {
	nameMap   map[string]netip.Addr
	ipsFull   bool
	subnet    netip.Prefix
	lowest    netip.Addr
	lastAlloc netip.Addr
}

func newDomainproxyAllocator(subnet netip.Prefix, lowest netip.Addr) *domainproxyAllocator {
	return &domainproxyAllocator{
		nameMap:   make(map[string]netip.Addr),
		ipsFull:   false,
		subnet:    subnet,
		lowest:    lowest,
		lastAlloc: lowest,
	}
}

type domainproxyRegistry struct {
	r               *mdnsRegistry
	dockerMachine   *Container
	bridgeLinkIndex int

	// maps domainproxy ips to container ips. we call container ips values
	ipMap map[netip.Addr]domainproxytypes.Upstream

	// maps domain names to domainproxy ips
	v4 *domainproxyAllocator
	v6 *domainproxyAllocator
}

func newDomainproxyRegistry(r *mdnsRegistry, subnet4 netip.Prefix, lowest4 netip.Addr, subnet6 netip.Prefix, lowest6 netip.Addr) domainproxyRegistry {
	return domainproxyRegistry{
		r:               r,
		dockerMachine:   nil,
		bridgeLinkIndex: -1,

		ipMap: make(map[netip.Addr]domainproxytypes.Upstream),

		v4: newDomainproxyAllocator(subnet4, lowest4),
		v6: newDomainproxyAllocator(subnet6, lowest6),
	}
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

func (d *domainproxyRegistry) freeAddrLocked(ip netip.Addr) {
	upstream, ok := d.ipMap[ip]
	if !ok || !upstream.IsValid() {
		return
	}

	d.ipMap[ip] = domainproxytypes.Upstream{IP: nil}

	prefix := "domainproxy4"
	if ip.Is6() {
		prefix = "domainproxy6"
	}

	if upstream.Docker {
		if d.dockerMachine != nil {
			_, err := withContainerNetns(d.dockerMachine, func() (struct{}, error) {
				return struct{}{}, nft.WithTable(nft.FamilyInet, netconf.NftableInet, func(conn *nftables.Conn, table *nftables.Table) error {
					err := nft.MapDeleteByName(conn, table, prefix, nft.IPAddr(ip))
					if err != nil {
						return err
					}
					// may not exist if never probed
					err = nft.SetDeleteByName(conn, table, prefix+"_probed", nft.IPAddr(ip))
					if err != nil && !errors.Is(err, unix.ENOENT) {
						return err
					}
					err = nft.SetDeleteByName(conn, table, prefix+"_masquerade", nft.Concat(nft.IP(upstream.IP), nft.IP(upstream.IP)))
					if err != nil {
						return err
					}
					return nil
				})
			})
			if err != nil {
				// this will happen if docker is not running -- very possible if the docker machine just shut down
				if d.dockerMachine.Running() {
					logrus.WithError(err).Error("failed to delete from docker domainproxy")
				}
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

		// may not exist if never probed
		err = nft.SetDeleteByName(conn, table, prefix+"_probed", nft.IPAddr(ip))
		if err != nil && !errors.Is(err, unix.ENOENT) {
			logrus.WithError(err).Error("failed to remove from domainproxy 3")
		}

		err = nft.SetDeleteByName(conn, table, prefix+"_masquerade", nft.Concat(nft.IPAddr(ip), nft.IP(upstream.IP)))
		if err != nil {
			logrus.WithError(err).Error("failed to remove from domainproxy 4")
		}

		return nil
	})
	if err != nil {
		logrus.WithError(err).Error("failed to remove from domainproxy 5")
	}

	err = nft.WithTable(nft.FamilyBridge, netconf.NftableBridge, func(conn *nftables.Conn, table *nftables.Table) error {
		return nft.SetDeleteByName(conn, table, prefix+"_masquerade_bridge", nft.Concat(nft.IPAddr(ip), nft.IP(upstream.IP)))
	})
	if err != nil {
		logrus.WithError(err).Error("failed to remove from domainproxy 6")
	}
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

	prefix := "domainproxy4"
	if ip.Is6() {
		prefix = "domainproxy6"
	}

	if val.Docker {
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

func (d *domainproxyRegistry) findNamesUpstreamLocked(allocator *domainproxyAllocator, names []string) (addr netip.Addr, upstream domainproxytypes.Upstream, ok bool) {
	// try to find something already claimed first
	for _, name := range names {
		addr, ok := allocator.nameMap[name]
		if !ok {
			continue
		}

		upstream, ok := d.ipMap[addr]
		if !ok || !upstream.IsValid() || !upstream.EqualNames(names) {
			// different upstream
			continue
		}

		return addr, upstream, true
	}

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

		return addr, domainproxytypes.Upstream{}, true
	}

	return netip.Addr{}, domainproxytypes.Upstream{}, false
}

func (d *domainproxyRegistry) assignUpstreamLocked(allocator *domainproxyAllocator, val domainproxytypes.Upstream) (addr netip.Addr, err error) {
	addr, currUpstream, ok := d.findNamesUpstreamLocked(allocator, val.Names)
	if !ok {
		// couldn't find a reclaimable ip or our upstream, allocate
		nextAddr, ok := d.nextAvailableIPLocked(allocator)
		if !ok {
			return netip.Addr{}, errNoMoreIPs
		}

		d.setAddrUpstreamLocked(nextAddr, val)
		for _, name := range val.Names {
			allocator.nameMap[name] = nextAddr
		}

		return nextAddr, nil
	}

	if !currUpstream.IsValid() {
		// we found a reclaimable ip and are taking it, so set names
		for _, name := range val.Names {
			allocator.nameMap[name] = addr
		}
	}
	d.setAddrUpstreamLocked(addr, val)

	return addr, nil
}

func (d *domainproxyRegistry) freeNamesLocked(names []string) {
	for _, name := range names {
		if addr, ok := d.v4.nameMap[name]; ok {
			if upstream, ok := d.ipMap[addr]; ok && upstream.EqualNames(names) {
				d.freeAddrLocked(addr)
			}
		}
		if addr, ok := d.v6.nameMap[name]; ok {
			if upstream, ok := d.ipMap[addr]; ok && upstream.EqualNames(names) {
				d.freeAddrLocked(addr)
			}
		}
	}
}

func (d *domainproxyRegistry) ensureMachineIPsCorrectLocked(names []string, machine *Container) (net.IP, net.IP) {
	var ip4 net.IP
	var ip6 net.IP

	valips, err := machine.GetIPAddrs()
	if err != nil {
		logrus.WithError(err).WithField("name", machine.Name).Debug("failed to get machine IPs for DNS")
		return nil, nil
	}

	for _, valip := range valips {
		if ip4 == nil && valip.To4() != nil {
			addr, err := d.assignUpstreamLocked(d.v4, domainproxytypes.Upstream{IP: valip, Names: names, Docker: false, ContainerID: machine.ID})
			if err != nil {
				logrus.WithError(err).WithField("name", machine.Name).Debug("failed to assign ip4 for DNS")
				continue
			}

			ip4 = addr.AsSlice()
		}

		if ip6 == nil && valip.To4() == nil {
			addr, err := d.assignUpstreamLocked(d.v6, domainproxytypes.Upstream{IP: valip, Names: names, Docker: false, ContainerID: machine.ID})
			if err != nil {
				logrus.WithError(err).WithField("name", machine.Name).Debug("failed to assign ip6 for DNS")
				continue
			}

			ip6 = addr.AsSlice()
		}
	}

	return ip4, ip6
}

func setupDomainProxyInterface() error {
	_, domainproxySubnet4, err := net.ParseCIDR(netconf.DomainproxySubnet4CIDR)
	if err != nil {
		return err
	}

	_, domainproxySubnet6, err := net.ParseCIDR(netconf.DomainproxySubnet6CIDR)
	if err != nil {
		return err
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	// this is an anyip route, which causes linux to treat the entire domainproxy subnet as its own ips
	route4 := netlink.Route{LinkIndex: lo.Attrs().Index, Dst: domainproxySubnet4, Type: unix.RTN_LOCAL, Scope: unix.RT_SCOPE_HOST, Table: 255}
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
	route6 := netlink.Route{LinkIndex: lo.Attrs().Index, Dst: domainproxySubnet6, Type: unix.RTN_LOCAL, Scope: unix.RT_SCOPE_HOST, Table: 255}
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
	r *mdnsRegistry
}

func (c *SconProxyCallbacks) GetUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error) {
	return c.r.getProxyUpstreamByHost(host, v4)
}

func (c *SconProxyCallbacks) GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	return c.r.getProxyUpstreamByAddr(addr)
}

func (c *SconProxyCallbacks) GetMark(upstream domainproxytypes.Upstream) int {
	mark := netconf.VmFwmarkTproxyOutboundBit
	if upstream.Docker {
		mark |= netconf.VmFwmarkDockerRouteBit
	}

	return mark
}

func (c *SconProxyCallbacks) NfqueueMarkReject(mark uint32) uint32 {
	return mark | netconf.VmFwmarkNfqueueRejectBit
}

func (c *SconProxyCallbacks) NfqueueMarkSkip(mark uint32) uint32 {
	return mark | netconf.VmFwmarkNfqueueSkipBit
}

func (c *SconProxyCallbacks) NftableName() string {
	return netconf.NftableInet
}

func (c *SconProxyCallbacks) GetMachineOpenPorts(machineID string) (map[uint16]struct{}, error) {
	return c.r.getMachineOpenPorts(machineID)
}

func (c *SconProxyCallbacks) GetContainerOpenPorts(containerID string) (map[uint16]struct{}, error) {
	if c.r.domainproxy.dockerMachine == nil {
		return map[uint16]struct{}{}, nil
	}

	ports := map[uint16]struct{}{}
	err := c.r.domainproxy.dockerMachine.UseAgent(func(client *agent.Client) error {
		var err error
		ports, err = client.DockerGetContainerOpenPorts(containerID)
		return err
	})
	if err != nil {
		return nil, err
	}

	return ports, nil
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

	upstream, ok := r.domainproxy.ipMap[proxyAddr]
	if !ok {
		return proxyAddr, domainproxytypes.Upstream{}, errors.New("could not find backend in mdns registry")
	}

	return proxyAddr, upstream, nil
}

func (r *mdnsRegistry) getProxyUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	upstream, ok := r.domainproxy.ipMap[addr]
	if !ok {
		return domainproxytypes.Upstream{}, errors.New("could not find backend in mdns registry")
	}

	return upstream, nil
}

func (r *mdnsRegistry) getMachineOpenPorts(machineID string) (map[uint16]struct{}, error) {
	machine, err := r.manager.GetByID(machineID)
	if err != nil {
		return nil, fmt.Errorf("get machine by id: %w", err)
	}

	openPorts := map[uint16]struct{}{}

	// always grab both v4 and v6 ports because dual stack shows up as ipv6 anyways, so not worth the effort to differentiate
	// especially when our probing routine should be relatively fast anyways, especially for non-listening ports
	netTcp4, err := withContainerNetns(machine, func() (string, error) {
		contents, err := os.ReadFile("/proc/thread-self/net/tcp")
		if err != nil {
			return "", fmt.Errorf("read tcp4: %w", err)
		}

		return string(contents), nil
	})
	if err != nil {
		return nil, err
	}

	netTcp6, err := withContainerNetns(machine, func() (string, error) {
		contents, err := os.ReadFile("/proc/thread-self/net/tcp6")
		if err != nil {
			return "", fmt.Errorf("read tcp6: %w", err)
		}

		return string(contents), nil
	})
	if err != nil {
		return nil, err
	}

	err = util.ParseNetTcpPorts(netTcp4, openPorts)
	if err != nil {
		return nil, err
	}

	err = util.ParseNetTcpPorts(netTcp6, openPorts)
	if err != nil {
		return nil, err
	}

	return openPorts, nil
}
