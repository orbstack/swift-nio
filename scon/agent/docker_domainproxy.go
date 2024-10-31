package agent

import (
	"fmt"
	"net/netip"

	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

func replaceIPBase(ip netip.Addr, base netip.Prefix) netip.Addr {
	ipSlice := ip.AsSlice()
	baseSlice := base.Masked().Addr().AsSlice()
	bits := base.Bits()
	for i, _ := range ipSlice {
		ipSlice[i] &= ^uint8(0) >> min(bits, 8)
		ipSlice[i] |= baseSlice[i]
		bits -= 8
		if bits <= 0 {
			break
		}
	}
	// we got ipSlice from .AsSlice so it must be either 4 or 16 bits in length, so we expect ok to be true
	ip, ok := netip.AddrFromSlice(ipSlice)
	if !ok {
		panic("unexpected length of slice from netip.Addr.AsSlice")
	}

	return ip
}

func (d *DockerAgent) startDomaintproxy() error {
	domainproxySubnet4Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet4CIDR)
	domainproxySubnet6Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet6CIDR)

	getMark := func(upstream domainproxytypes.DomainproxyUpstream) int {
		return netconf.DockerFwmarkTproxyOutbound
	}

	proxy, err := domainproxy.NewDomainTLSProxy(d.host, d.scon.GetProxyUpstream, getMark)
	if err != nil {
		return fmt.Errorf("create tls domainproxy: %w", err)
	}
	d.domaintproxy = proxy

	err = proxy.Start(netconf.VnetTproxyIP4, netconf.VnetTproxyIP6, domainproxySubnet4Prefix, domainproxySubnet6Prefix)
	if err != nil {
		return err
	}

	logrus.Debug("started docker tls domaintproxy")

	return nil
}
