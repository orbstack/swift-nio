package main

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"

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

	// maps domain names to domainproxy ips
	nameMap4   map[string]netip.Addr
	ipsFull4   bool
	subnet4    netip.Prefix
	lowest4    netip.Addr
	lastAlloc4 netip.Addr

	nameMap6   map[string]netip.Addr
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

		nameMap4:   make(map[string]netip.Addr),
		ipsFull4:   false,
		subnet4:    subnet4.Masked(),
		lowest4:    lowest4,
		lastAlloc4: lowest4,

		nameMap6:   make(map[string]netip.Addr),
		ipsFull6:   false,
		subnet6:    subnet6.Masked(),
		lowest6:    lowest6,
		lastAlloc6: lowest6,
	}
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
	if val.IP == nil {
		if upstream, has := d.ipMap[ip]; has && upstream.IP != nil {
			d.ipMap[ip] = domainproxytypes.DomainproxyUpstream{IP: nil}

			if upstream.Docker {
				if d.dockerMachine != nil {
					_, err := withContainerNetns(d.dockerMachine, func() (struct{}, error) {
						var err error

						if ip.Is4() {
							err = nft.Run("delete", "element", "inet", "orbstack", "domainproxy4", fmt.Sprintf("{ %v }", ip))
							if err != nil {
								return struct{}{}, err
							}
							err = nft.Run("delete", "element", "inet", "orbstack", "domainproxy4_masquerade", fmt.Sprintf("{ %v . %v }", upstream.IP, upstream.IP))
							if err != nil {
								return struct{}{}, err
							}
						}

						if ip.Is6() {
							err = nft.Run("delete", "element", "inet", "orbstack", "domainproxy6", fmt.Sprintf("{ %v }", ip))
							if err != nil {
								return struct{}{}, err
							}
							err = nft.Run("delete", "element", "inet", "orbstack", "domainproxy6_masquerade", fmt.Sprintf("{ %v . %v }", upstream.IP, upstream.IP))
							if err != nil {
								return struct{}{}, err
							}
						}

						return struct{}{}, nil
					})

					if err != nil {
						logrus.WithError(err).Error("could not delete from domainproxy in docker")
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
				err = nft.Run("delete", "element", "inet", "vm", "domainproxy4_masquerade", fmt.Sprintf("{ %v . %v }", ip, upstream.IP))
			} else if ip.Is6() {
				err = nft.Run("delete", "element", "inet", "vm", "domainproxy6_masquerade", fmt.Sprintf("{ %v . %v }", ip, upstream.IP))
			}
			if err != nil {
				logrus.WithError(err).Debug("could not remove from domainproxy_masquerade map")
			}

			if ip.Is4() {
				err = nft.Run("delete", "element", "bridge", "vm_bridge", "domainproxy4_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, upstream.IP))
			} else if ip.Is6() {
				err = nft.Run("delete", "element", "bridge", "vm_bridge", "domainproxy6_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, upstream.IP))
			}
			if err != nil {
				logrus.WithError(err).Debug("could not remove from domainproxy_masquerade_bridge map")
			}
		}
		return
	}

	// make sure the element gets removed before we change it to something else
	if currVal, has := d.ipMap[ip]; has && !currVal.IP.Equal(val.IP) {
		d.setAddrLocked(ip, domainproxytypes.DomainproxyUpstream{IP: nil})
	}

	if val.Docker {
		if d.dockerMachine != nil {
			_, err := withContainerNetns(d.dockerMachine, func() (struct{}, error) {
				var err error
				if ip.Is4() {
					err = nft.Run("add", "element", "inet", "orbstack", "domainproxy4", fmt.Sprintf("{ %v : %v }", ip, val.IP))
					if err != nil {
						return struct{}{}, err
					}
					err = nft.Run("add", "element", "inet", "orbstack", "domainproxy4_masquerade", fmt.Sprintf("{ %v . %v }", val.IP, val.IP))
					if err != nil {
						return struct{}{}, err
					}
				}

				if ip.Is6() {
					err = nft.Run("add", "element", "inet", "orbstack", "domainproxy6", fmt.Sprintf("{ %v : %v }", ip, val.IP))
					if err != nil {
						return struct{}{}, err
					}
					err = nft.Run("add", "element", "inet", "orbstack", "domainproxy6_masquerade", fmt.Sprintf("{ %v . %v }", val.IP, val.IP))
					if err != nil {
						return struct{}{}, err
					}
				}

				return struct{}{}, nil
			})
			if err != nil {
				logrus.WithError(err).Error("failed to add to domainproxy in docker")
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
		err = nft.Run("add", "element", "inet", "vm", "domainproxy4", fmt.Sprintf("{ %v : %v }", ip, val.IP))
	} else if ip.Is6() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy6", fmt.Sprintf("{ %v : %v }", ip, val.IP))
	}
	if err != nil {
		logrus.WithError(err).Debug("could not add to domainproxy map")
	}

	if ip.Is4() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy4_masquerade", fmt.Sprintf("{ %v . %v }", ip, val.IP))
	} else if ip.Is6() {
		err = nft.Run("add", "element", "inet", "vm", "domainproxy6_masquerade", fmt.Sprintf("{ %v . %v }", ip, val.IP))
	}
	if err != nil {
		logrus.WithError(err).Error("failed to add to domainproxy_masquerade")
	}
	if ip.Is4() {
		err = nft.Run("add", "element", "bridge", "vm_bridge", "domainproxy4_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, val.IP))
	} else if ip.Is6() {
		err = nft.Run("add", "element", "bridge", "vm_bridge", "domainproxy6_masquerade_bridge", fmt.Sprintf("{ %v . %v }", ip, val.IP))
	}
	if err != nil {
		logrus.WithError(err).Error("failed to add to domainproxy_masquerade")
	}

	d.ipMap[ip] = val
}

func (d *domainproxyInfo) setIPLocked(ip net.IP, val domainproxytypes.DomainproxyUpstream) {
	d.setAddrLocked(mustAddrFromSlice(ip), val)
}

func (d *domainproxyInfo) getAddrLocked(ip netip.Addr) (domainproxytypes.DomainproxyUpstream, bool) {
	val, has := d.ipMap[ip]
	return val, has
}

func (d *domainproxyInfo) Locked(ip net.IP) (domainproxytypes.DomainproxyUpstream, bool) {
	return d.getAddrLocked(mustAddrFromSlice(ip))
}

func nextAvailableIPLocked(ipMap map[netip.Addr]domainproxytypes.DomainproxyUpstream, subnet netip.Prefix, lowest netip.Addr, lastAlloc *netip.Addr, ipsFull *bool) (ip netip.Addr, ok bool) {
	ip = *lastAlloc

	var freeableIP netip.Addr
	foundFreeableIP := false

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
		if !foundFreeableIP && val.IP == nil {
			freeableIP = ip
			foundFreeableIP = true
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

	if foundFreeableIP {
		*lastAlloc = freeableIP
		return freeableIP, true
	} else {
		return ip, false
	}
}

func slicesEqualUnordered[T cmp.Ordered](s1 []T, s2 []T) bool {
	if len(s1) != len(s2) {
		return false
	}

	return slices.Equal(
		slices.Sorted(slices.Values(s1)),
		slices.Sorted(slices.Values(s2)),
	)
}

func (d *domainproxyInfo) claimOrUpdateIP4Locked(val domainproxytypes.DomainproxyUpstream) (ip netip.Addr, ok bool) {
	if val.IP == nil {
		return netip.Addr{}, false
	}

	for _, name := range val.Names {
		if preferredAddr, has := d.nameMap4[name]; has {
			if preferredAddrVal, has := d.getAddrLocked(preferredAddr); !has || preferredAddrVal.IP == nil {
				// our preferred address isn't claimed, let's take it
				d.setAddrLocked(preferredAddr, val)
				for _, setName := range val.Names {
					d.nameMap4[setName] = preferredAddr
				}
				logrus.WithFields(logrus.Fields{"name": name, "ip": preferredAddr, "val": val}).Debug("mdns domainproxy: claimed preferred ip")
				return preferredAddr, true
			} else {
				// our preferred address is already claimed. is that us or is that a different upstream?
				if slicesEqualUnordered(preferredAddrVal.Names, val.Names) {
					if preferredAddrVal.IP.Equal(val.IP) && preferredAddrVal.Docker == val.Docker {
						return preferredAddr, true
					}
					d.setAddrLocked(preferredAddr, val)
					logrus.WithFields(logrus.Fields{"addr": preferredAddr, "val": val}).Debug("mdns domainproxy: updated addr upstream")
					return preferredAddr, true
				} else {
					logrus.WithField("preferredAddr", preferredAddr).Debug("mdns domainproxy: could not assign preferred ip")
				}
			}
		}
	}

	if nextAddr, ok := nextAvailableIPLocked(d.ipMap, d.subnet4, d.lowest4, &d.lastAlloc4, &d.ipsFull4); ok {
		d.setAddrLocked(nextAddr, val)
		for _, setName := range val.Names {
			d.nameMap4[setName] = nextAddr
		}

		logrus.WithFields(logrus.Fields{"ip": nextAddr, "val": val}).Debug("mdns domainproxy: claimed available ip")
		return nextAddr, true
	}

	logrus.WithFields(logrus.Fields{"val": val}).Warn("mdns domainproxy: failed to claim an ip")
	return netip.Addr{}, false
}

func (d *domainproxyInfo) claimOrUpdateIP6Locked(val domainproxytypes.DomainproxyUpstream) (ip netip.Addr, ok bool) {
	if val.IP == nil {
		return netip.Addr{}, false
	}

	for _, name := range val.Names {
		if preferredAddr, has := d.nameMap6[name]; has {
			if preferredAddrVal, has := d.getAddrLocked(preferredAddr); !has || preferredAddrVal.IP == nil {
				// our preferred address isn't claimed, let's take it
				d.setAddrLocked(preferredAddr, val)
				for _, setName := range val.Names {
					d.nameMap6[setName] = preferredAddr
				}
				logrus.WithFields(logrus.Fields{"name": name, "ip": preferredAddr, "val": val}).Debug("mdns domainproxy: claimed preferred ip")
				return preferredAddr, true
			} else {
				// our preferred address is already claimed. is that us or is that a different upstream?
				if slicesEqualUnordered(preferredAddrVal.Names, val.Names) {
					if preferredAddrVal.IP.Equal(val.IP) && preferredAddrVal.Docker == val.Docker {
						return preferredAddr, true
					}
					d.setAddrLocked(preferredAddr, val)
					logrus.WithFields(logrus.Fields{"addr": preferredAddr, "val": val}).Debug("mdns domainproxy: updated addr upstream")
					return preferredAddr, true
				} else {
					logrus.WithField("preferredAddr", preferredAddr).Debug("mdns domainproxy: could not assign preferred ip")
				}
			}
		}
	}

	if nextAddr, ok := nextAvailableIPLocked(d.ipMap, d.subnet6, d.lowest6, &d.lastAlloc6, &d.ipsFull6); ok {
		d.setAddrLocked(nextAddr, val)
		for _, setName := range val.Names {
			d.nameMap6[setName] = nextAddr
		}

		logrus.WithFields(logrus.Fields{"ip": nextAddr, "val": val}).Debug("mdns domainproxy: claimed available ip")
		return nextAddr, true
	}

	logrus.WithFields(logrus.Fields{"val": val}).Warn("mdns domainproxy: failed to claim an ip")
	return netip.Addr{}, false
}

func (d *domainproxyInfo) freeNamesLocked(names []string) {
	for _, name := range names {
		if addr, has := d.nameMap4[name]; has {
			if upstream, has := d.getAddrLocked(addr); has && slicesEqualUnordered(upstream.Names, names) {
				d.setAddrLocked(addr, domainproxytypes.DomainproxyUpstream{IP: nil})
			}
		}
		if addr, has := d.nameMap6[name]; has {
			if upstream, has := d.getAddrLocked(addr); has && slicesEqualUnordered(upstream.Names, names) {
				d.setAddrLocked(addr, domainproxytypes.DomainproxyUpstream{IP: nil})
			}
		}
	}
}

func (d *domainproxyInfo) ensureMachineDomainproxyCorrectLocked(names []string, machine *Container) (net.IP, net.IP) {
	var ip4 net.IP
	var ip6 net.IP

	valips, err := machine.GetIPAddrs()
	if err == nil {
		for _, valip := range valips {
			if ip4 == nil && valip.To4() != nil {
				addr, ok := d.claimOrUpdateIP4Locked(domainproxytypes.DomainproxyUpstream{IP: valip, Names: names, Docker: false})
				if ok {
					ip4 = addr.AsSlice()
				}
			}
			if ip6 == nil && valip.To4() == nil {
				addr, ok := d.claimOrUpdateIP6Locked(domainproxytypes.DomainproxyUpstream{IP: valip, Names: names, Docker: false})
				if ok {
					ip6 = addr.AsSlice()
				}
			}
		}
	} else {
		logrus.WithError(err).WithField("name", machine.Name).Debug("failed to get machine IPs for DNS.")
	}

	return ip4, ip6
}

func setupDomainProxyInterface(mtu int) error {
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
