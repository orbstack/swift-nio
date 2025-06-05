package main

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
)

var (
	sconSubnet4 = netip.MustParsePrefix(netconf.SconSubnet4CIDR)
	sconSubnet6 = netip.MustParsePrefix(netconf.SconSubnet6CIDR)

	sconDocker4 = net.ParseIP(netconf.SconDockerIP4)
	sconDocker6 = net.ParseIP(netconf.SconDockerIP6)

	sconDocker4Addr = netip.MustParseAddr(netconf.SconDockerIP4)
	sconDocker6Addr = netip.MustParseAddr(netconf.SconDockerIP6)

	ErrNoIPAddress = errors.New("no IP address found")
)

func (c *Container) GetIPAddrs() ([]netip.Addr, error) {
	rt, err := c.RuntimeState()
	if err != nil {
		return []netip.Addr{}, err
	}

	rt.ipAddrsMu.Lock()
	defer rt.ipAddrsMu.Unlock()

	return rt.getIPAddrsLocked(c)
}

func (rt *ContainerRuntimeState) getIPAddrsLocked(c *Container) ([]netip.Addr, error) {
	if rt.ipAddrs != nil {
		return rt.ipAddrs, nil
	}

	ipStrs, err := c.lxc.IPAddresses()
	if err != nil {
		return nil, err
	}

	newIPs := make([]netip.Addr, 0, len(ipStrs))
	for _, ipStr := range ipStrs {
		ip, err := netip.ParseAddr(ipStr)
		if err != nil {
			return nil, fmt.Errorf("invalid IP address %q", ipStr)
		}
		// only return the IPs we issued
		// otherwise all the Docker gateway IPs get returned
		if sconSubnet4.Contains(ip) || sconSubnet6.Contains(ip) {
			newIPs = append(newIPs, ip)
		}
	}

	// if less than 2 ips (v4 and v6), don't cache it. this is a bad read
	if len(newIPs) >= 2 {
		rt.ipAddrs = newIPs
	}
	// ... but still return whatever we got
	return newIPs, nil
}

func (c *Container) getIP4() (netip.Addr, error) {
	// fastpath for static IPs (nftables forward cares about perf)
	if c.ID == ContainerIDDocker {
		return sconDocker4Addr, nil
	}

	ips, err := c.GetIPAddrs()
	if err != nil {
		return netip.Addr{}, err
	}

	for _, ip := range ips {
		if ip.Is4() {
			return ip, nil
		}
	}

	return netip.Addr{}, ErrNoIPAddress
}

func (c *Container) getIP6() (netip.Addr, error) {
	// fastpath for static IPs (nftables forward cares about perf)
	if c.ID == ContainerIDDocker {
		return sconDocker6Addr, nil
	}

	ips, err := c.GetIPAddrs()
	if err != nil {
		return netip.Addr{}, err
	}

	for _, ip := range ips {
		if ip.Is6() {
			return ip, nil
		}
	}

	return netip.Addr{}, ErrNoIPAddress
}
