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

type DockerProxyCallbacks struct {
	d *DockerAgent
}

func (cb *DockerProxyCallbacks) GetUpstreamByHost(host string, v4 bool) (domainproxytypes.Upstream, error) {
	return cb.d.scon.GetProxyUpstreamByHost(host, v4)
}

func (cb *DockerProxyCallbacks) GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	return cb.d.scon.GetProxyUpstreamByAddr(addr)
}

func (cb *DockerProxyCallbacks) GetMark(upstream domainproxytypes.Upstream) int {
	return netconf.DockerFwmarkTproxyOutbound
}

func (cb *DockerProxyCallbacks) NfqueueMarkReject(mark uint32) uint32 {
	return netconf.DockerFwmarkNfqueueReject
}

func (cb *DockerProxyCallbacks) NftableName() string {
	return netconf.NftableInet
}

func (d *DockerAgent) startDomainTLSProxy() error {
	domainproxySubnet4Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet4CIDR)
	domainproxySubnet6Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet6CIDR)

	proxy, err := domainproxy.NewDomainTLSProxy(d.host, &DockerProxyCallbacks{d: d})
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
		// TODO: migrate to nft library
		err = nft.Run("add", "rule", "inet", netconf.NftableInet, "dynamic-tlsproxy", "jump tlsproxy")
	} else if d.domainTLSProxyActive && !enabled {
		// we need to deactivate it
		err = nft.FlushChain(nft.FamilyInet, netconf.NftableInet, "dynamic-tlsproxy")
	}
	if err != nil {
		return err
	}

	d.domainTLSProxyActive = enabled
	return nil
}
