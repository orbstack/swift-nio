package agent

import (
	"fmt"
	"net/netip"

	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

func (d *DockerAgent) startDomainTLSProxy() error {
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
