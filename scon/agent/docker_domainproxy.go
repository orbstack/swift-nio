package agent

import (
	"fmt"
	"net/netip"

	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

func (d *DockerAgent) startDomainTLSProxy() error {
	domainproxySubnet4Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet4CIDR)
	domainproxySubnet6Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet6CIDR)

	getMark := func(upstream domainproxytypes.Upstream) int {
		return netconf.DockerFwmarkTproxyOutbound
	}

	proxy, err := domainproxy.NewDomainTLSProxy(d.host, d.scon.GetProxyUpstream, getMark)
	if err != nil {
		return fmt.Errorf("create tls domainproxy: %w", err)
	}
	d.domainTLSProxy = proxy

	err = proxy.Start(netconf.VnetTproxyIP4, netconf.VnetTproxyIP6, domainproxySubnet4Prefix, domainproxySubnet6Prefix)
	if err != nil {
		return err
	}

	logrus.Debug("started docker tls domaintproxy")

	return nil
}

func (d *DockerAgent) updateTLSProxyNftables(enabled bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var err error
	if !d.domainTLSProxyActive && enabled {
		// we need to activate it
		err = nft.Run("add", "rule", "inet", "orbstack", "prerouting-dynamic-tlsproxy", "jump prerouting-tlsproxy")
	} else if d.domainTLSProxyActive && !enabled {
		// we need to deactivate it
		err = nft.Run("flush", "chain", "inet", "orbstack", "prerouting-dynamic-tlsproxy")
	}
	if err != nil {
		return err
	}

	d.domainTLSProxyActive = enabled
	return nil
}
