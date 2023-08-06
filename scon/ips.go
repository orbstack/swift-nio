package main

import (
	"fmt"
	"net"
)

func (c *Container) GetIPAddresses() ([]net.IP, error) {
	// race is OK as long as it doesn't race with writer (start/stop)
	c.mu.RLock()
	defer c.mu.RUnlock()

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
		parsed := net.ParseIP(ipStr)
		if parsed == nil {
			return nil, fmt.Errorf("invalid IP address %q", ipStr)
		}
		newIPs = append(newIPs, parsed)
	}

	// if less than 2 ips (v4 and v6), don't cache it. this is a bad read
	if len(newIPs) >= 2 {
		c.ipAddrs = newIPs
	}
	// ... but still return whatever we got
	return newIPs, nil
}
