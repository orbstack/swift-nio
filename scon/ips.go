package main

import (
	"fmt"
	"net"

	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
)

var (
	sconSubnet4 = mustParseCIDR(netconf.SconSubnet4CIDR)
	sconSubnet6 = mustParseCIDR(netconf.SconSubnet6CIDR)

	sconDocker4 = net.ParseIP(netconf.SconDockerIP4)
	sconDocker6 = net.ParseIP(netconf.SconDockerIP6)
)

func (c *Container) GetIPAddrs() ([]net.IP, error) {
	// race is OK as long as it doesn't race with writer (start/stop)
	c.ipAddrsMu.Lock()
	defer c.ipAddrsMu.Unlock()

	return c.getIPAddrsLocked()
}

func (c *Container) getIP4Locked() (net.IP, error) {
	// fastpath for iptables forward
	if c.ID == ContainerIDDocker {
		return sconDocker4, nil
	}

	ips, err := c.getIPAddrsLocked()
	if err != nil {
		return nil, err
	}

	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}

	return nil, fmt.Errorf("no IPv4 address found")
}

func (c *Container) getIP6Locked() (net.IP, error) {
	// fastpath for iptables forward
	if c.ID == ContainerIDDocker {
		return sconDocker6, nil
	}

	ips, err := c.getIPAddrsLocked()
	if err != nil {
		return nil, err
	}

	for _, ip := range ips {
		if ip.To4() == nil {
			return ip, nil
		}
	}

	return nil, fmt.Errorf("no IPv6 address found")
}

func (c *Container) getIPAddrsLocked() ([]net.IP, error) {
	oldIPs := c.ipAddrs
	if oldIPs != nil {
		return oldIPs, nil
	}

	ipStrs, err := c.lxc.IPAddresses()
	if err != nil {
		return nil, err
	}

	newIPs := make([]net.IP, 0, len(ipStrs))
	for _, ipStr := range ipStrs {
		ip := net.ParseIP(ipStr)
		if ip == nil {
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
		c.ipAddrs = newIPs
	}
	// ... but still return whatever we got
	return newIPs, nil
}
