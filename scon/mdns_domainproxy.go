package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type domainproxyInfo struct {
	r               *mdnsRegistry
	dockerMachine   *Container
	conbr0LinkIndex int

	// maps domainproxy ips to container ips. we call container ips values
	ipMap map[netip.Addr]domainproxytypes.DomainproxyUpstream

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

func newDomainproxyInfo(r *mdnsRegistry, subnet4 netip.Prefix, lowest4 netip.Addr, subnet6 netip.Prefix, lowest6 netip.Addr) domainproxyInfo {
	return domainproxyInfo{
		r:               r,
		dockerMachine:   nil,
		conbr0LinkIndex: -1,

		ipMap: make(map[netip.Addr]domainproxytypes.DomainproxyUpstream),

		idMap4:     make(map[string]netip.Addr),
		ipsFull4:   false,
		subnet4:    subnet4.Masked(),
		lowest4:    lowest4,
		lastAlloc4: lowest4,

		idMap6:     make(map[string]netip.Addr),
		ipsFull6:   false,
		subnet6:    subnet6.Masked(),
		lowest6:    lowest6,
		lastAlloc6: lowest6,
	}
}

func (d *domainproxyInfo) getDockerMachine() *Container {
	if d.dockerMachine != nil {
		return d.dockerMachine
	}

	dockerMachine, err := d.r.manager.GetByID(ContainerIDDocker)
	if err != nil {
		logrus.WithError(err).Error("unable to get docker machine")
		return nil
	}
	d.dockerMachine = dockerMachine

	return dockerMachine
}

func (d *domainproxyInfo) addNeighbor(ip netip.Addr) {
	if d.conbr0LinkIndex < 0 {
		conbr0, err := netlink.LinkByName(ifBridge)
		if err != nil {
			logrus.Debug("unable to get conbr0 link: %w", err)
			return
		}
		d.conbr0LinkIndex = conbr0.Attrs().Index
	}

	var err error
	if ip.Is6() {
		err = netlink.NeighAdd(&netlink.Neigh{Family: unix.AF_INET6, Flags: netlink.NTF_PROXY, State: netlink.NUD_PERMANENT, Type: unix.RTN_UNSPEC, LinkIndex: d.conbr0LinkIndex, IP: ip.AsSlice()})
	}
	if err != nil && !errors.Is(err, unix.EEXIST) {
		logrus.Debug("failed to add neighbor: %w", err)
	}
}

func (d *domainproxyInfo) setAddrLocked(ip netip.Addr, val domainproxytypes.DomainproxyUpstream) {
	if val.Ip == nil {
		if upstream, has := d.ipMap[ip]; has && upstream.Ip != nil {
			d.ipMap[ip] = domainproxytypes.DomainproxyUpstream{Ip: nil}

			if upstream.Docker {
				if dockerMachine := d.getDockerMachine(); dockerMachine != nil {
					err := dockerMachine.UseAgent(func(a *agent.Client) error {
						return a.DockerRemoveDomainproxy(ip)
					})
					if err != nil {
						logrus.WithError(err).Warn("failed to remove domainproxy from docker machine")
					}
				}

				var err error
				if ip.Is4() {
					err = nft.Run("delete", "element", "inet", "vm", "domainproxy4_docker", fmt.Sprintf("{ %v }", ip))
				} else if ip.Is6() {
					err = nft.Run("delete", "element", "inet", "vm", "domainproxy6_docker", fmt.Sprintf("{ %v }", ip))
				}
				if err != nil {
					logrus.WithError(err).Error("could not delete from domainproxy_docker")
				}
			}

			var err error
			if ip.Is4() {
				err = nft.Run("delete", "element", "inet", "vm", "domainproxy4", fmt.Sprintf("{ %v }", ip))
			} else if ip.Is6() {
				err = nft.Run("delete", "element", "inet", "vm", "domainproxy6", fmt.Sprintf("{ %v }", ip))
			}
			if err != nil {
				logrus.WithError(err).Debug("could not remove from domainproxy map")
			}

			if ip.Is4() {
				err = nft.Run("delete", "element", "inet", "vm", "domainproxy4_masquerade", fmt.Sprintf("{ %v . %v }", ip, upstream.Ip))
			} else if ip.Is6() {
				err = nft.Run("delete", "element", "inet", "vm", "domainproxy6_masquerade", fmt.Sprintf("{ %v . %v }", ip, upstream.Ip))
			}
			if err != nil {
				logrus.WithError(err).Debug("could not remove from domainproxy_masquerade map")
			}

			if ip.Is4() {
				err = nft.Run("delete", "element", "bridge", "vm_bridge", "domainproxy4_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, upstream.Ip))
			} else if ip.Is6() {
				err = nft.Run("delete", "element", "bridge", "vm_bridge", "domainproxy6_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, upstream.Ip))
			}
			if err != nil {
				logrus.WithError(err).Debug("could not remove from domainproxy_masquerade_bridge map")
			}
		}
		return
	}

	// make sure the element gets removed before we change it to something else
	if currVal, has := d.ipMap[ip]; has && !currVal.Ip.Equal(val.Ip) {
		d.setAddrLocked(ip, domainproxytypes.DomainproxyUpstream{Ip: nil})
	}

	if val.Docker {
		if dockerMachine := d.getDockerMachine(); dockerMachine != nil {
			err := dockerMachine.UseAgent(func(a *agent.Client) error {
				return a.DockerAddDomainproxy(agent.DockerAddDomainproxyArgs{Ip: ip, Val: val.Ip})
			})
			if err != nil {
				logrus.WithError(err).Debug("failed to add domainproxy to docker machine")
			}
		}

		var err error
		if ip.Is4() {
			err = nft.Run("add", "element", "inet", "vm", "domainproxy4_docker", fmt.Sprintf("{ %v }", ip))
		} else if ip.Is6() {
			err = nft.Run("add", "element", "inet", "vm", "domainproxy6_docker", fmt.Sprintf("{ %v }", ip))
		}
		if err != nil {
			logrus.WithError(err).Error("failed to add to domainproxy_docker")
		}
	}

	go d.addNeighbor(ip)

	var err error
	if ip.Is4() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy4", fmt.Sprintf("{ %v : %v }", ip, val.Ip))
	} else if ip.Is6() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy6", fmt.Sprintf("{ %v : %v }", ip, val.Ip))
	}
	if err != nil {
		logrus.WithError(err).Debug("could not add to domainproxy map")
	}

	if ip.Is4() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy4_masquerade", fmt.Sprintf("{ %v . %v }", ip, val.Ip))
	} else if ip.Is6() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy6_masquerade", fmt.Sprintf("{ %v . %v }", ip, val.Ip))
	}
	if err != nil {
		logrus.WithError(err).Error("failed to add to domainproxy_masquerade")
	}
	if ip.Is4() {
		err = nft.Run("add", "element", "bridge", "vm_bridge", "domainproxy4_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, val.Ip))
	} else if ip.Is6() {
		err = nft.Run("add", "element", "bridge", "vm_bridge", "domainproxy6_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, val.Ip))
	}
	if err != nil {
		logrus.WithError(err).Error("failed to add to domainproxy_masquerade")
	}

	d.ipMap[ip] = val
}

func (d *domainproxyInfo) setIpLocked(ip net.IP, val domainproxytypes.DomainproxyUpstream) {
	d.setAddrLocked(mustAddrFromSlice(ip), val)
}

func (d *domainproxyInfo) getAddrLocked(ip netip.Addr) (domainproxytypes.DomainproxyUpstream, bool) {
	val, has := d.ipMap[ip]
	return val, has
}

func (d *domainproxyInfo) Locked(ip net.IP) (domainproxytypes.DomainproxyUpstream, bool) {
	return d.getAddrLocked(mustAddrFromSlice(ip))
}

func nextAvailableIpLocked(ipMap map[netip.Addr]domainproxytypes.DomainproxyUpstream, subnet netip.Prefix, lowest netip.Addr, lastAlloc *netip.Addr, ipsFull *bool) (ip netip.Addr, ok bool) {
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
		if !foundFreeableIp && val.Ip == nil {
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

func (d *domainproxyInfo) claimNextAvailableIp4Locked(id string, val domainproxytypes.DomainproxyUpstream) (ip netip.Addr, ok bool) {
	if val.Ip == nil {
		return netip.Addr{}, false
	}

	if preferredAddr, has := d.idMap4[id]; has {
		if preferredAddrVal, has := d.getAddrLocked(preferredAddr); has && preferredAddrVal.Ip == nil {
			d.setAddrLocked(preferredAddr, val)
			// id map already has the right value
			logrus.WithFields(logrus.Fields{"id": id, "ip": preferredAddr, "val": val}).Debug("mdns domainproxy: claimed preferred ip")
			return preferredAddr, true
		} else {
			logrus.WithField("preferredAddr", preferredAddr).Debug("mdns domainproxy: could not assign preferred ip")
		}
	}

	if nextAddr, ok := nextAvailableIpLocked(d.ipMap, d.subnet4, d.lowest4, &d.lastAlloc4, &d.ipsFull4); ok {
		d.setAddrLocked(nextAddr, val)
		d.idMap4[id] = nextAddr

		logrus.WithFields(logrus.Fields{"id": id, "ip": nextAddr, "val": val}).Debug("mdns domainproxy: claimed available ip")
		return nextAddr, true
	}

	logrus.WithFields(logrus.Fields{"id": id, "val": val}).Warn("mdns domainproxy: failed to claim an ip")
	return netip.Addr{}, false
}

func (d *domainproxyInfo) claimNextAvailableIp6Locked(id string, val domainproxytypes.DomainproxyUpstream) (ip netip.Addr, ok bool) {
	if val.Ip == nil {
		return netip.Addr{}, false
	}

	if preferredAddr, has := d.idMap6[id]; has {
		if preferredAddrVal, has := d.getAddrLocked(preferredAddr); has && preferredAddrVal.Ip == nil {
			d.setAddrLocked(preferredAddr, val)
			// id map already has the right value
			logrus.WithFields(logrus.Fields{"id": id, "ip": preferredAddr, "val": val}).Debug("mdns domainproxy: claimed preferred ip")
			return preferredAddr, true
		} else {
			logrus.WithFields(logrus.Fields{"id": id, "preferredAddr": preferredAddr, "val": val}).Debug("mdns domainproxy: could not assign preferred ip")
		}
	}

	if nextAddr, ok := nextAvailableIpLocked(d.ipMap, d.subnet6, d.lowest6, &d.lastAlloc6, &d.ipsFull6); ok {
		d.setAddrLocked(nextAddr, val)
		d.idMap6[id] = nextAddr

		logrus.WithFields(logrus.Fields{"id": id, "ip": nextAddr, "val": val}).Debug("mdns domainproxy: claimed available ip.")
		return nextAddr, true
	}

	logrus.WithFields(logrus.Fields{"id": id, "val": val}).Warn("mdns domainproxy: failed to claim an ip")
	return netip.Addr{}, false
}

func (d *domainproxyInfo) ensureMachineDomainproxyCorrectLocked(id string, machine *Container) (ip4 net.IP, ip6 net.IP) {
	// prevent us from trying to do stuff with ids that don't make sense
	if id == "" {
		return
	}

	valips, err := machine.GetIPAddrs()
	if err == nil {
		for _, valip := range valips {
			if ip4 != nil && valip.To4() != nil {
				continue
			}

			if ip6 != nil && valip.To4() == nil {
				continue
			}

			var addr netip.Addr
			var is4 bool
			if valip.To4() != nil {
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
				if currentVal, has := d.getAddrLocked(addr); has && currentVal.Id == id {
					if is4 {
						ip4 = addr.AsSlice()
					} else {
						ip6 = addr.AsSlice()
					}

					if !currentVal.Ip.Equal(valip) {
						logrus.WithFields(logrus.Fields{"id": id, "machine": machine, "currentVal": currentVal, "val": valip}).Debug("mdns domainproxy: entry wrong")
						d.setAddrLocked(addr, domainproxytypes.DomainproxyUpstream{Ip: valip, Id: id, Docker: false})
					}
					continue
				}
			}

			// if we didn't hit the continue, then we didnt have an ip
			if is4 {
				if ip, ok := d.claimNextAvailableIp4Locked(id, domainproxytypes.DomainproxyUpstream{Ip: valip, Id: id, Docker: false}); ok {
					ip4 = ip.AsSlice()
				}
			} else {
				if ip, ok := d.claimNextAvailableIp6Locked(id, domainproxytypes.DomainproxyUpstream{Ip: valip, Id: id, Docker: false}); ok {
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

	_, domainproxySubnet6, err := net.ParseCIDR(netconf.DomainproxySubnet6Cidr)
	if err != nil {
		return err
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

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
		return fmt.Errorf("adding route: %w", err)
	}

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
		return fmt.Errorf("adding route: %w", err)
	}

	err = os.WriteFile("/proc/sys/net/ipv6/conf/conbr0/proxy_ndp", []byte("1"), 0)
	if err != nil {
		return fmt.Errorf("enable proxy ndp: %w", err)
	}

	return nil
}
