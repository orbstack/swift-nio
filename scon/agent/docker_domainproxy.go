package agent

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util/dirfs"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

type DockerProxyCallbacks struct {
	d *DockerAgent
}

func (cb *DockerProxyCallbacks) GetUpstreamByHost(host string, v4 bool) (netip.Addr, domainproxytypes.Upstream, error) {
	return cb.d.scon.GetProxyUpstreamByHost(host, v4)
}

func (cb *DockerProxyCallbacks) GetUpstreamByAddr(addr netip.Addr) (domainproxytypes.Upstream, error) {
	return cb.d.scon.GetProxyUpstreamByAddr(addr)
}

func (cb *DockerProxyCallbacks) GetMark(upstream domainproxytypes.Upstream) int {
	return netconf.DockerFwmarkTproxyOutbound
}

func (cb *DockerProxyCallbacks) NfqueueMarkSkip(mark uint32) uint32 {
	return netconf.DockerFwmarkNfqueueSkip
}

func (cb *DockerProxyCallbacks) NftableName() string {
	return netconf.NftableInet
}

func (cb *DockerProxyCallbacks) GetHostOpenPorts(host domainproxytypes.Host) (map[uint16]struct{}, error) {
	// docker machine's domainproxy only proxies other docker containers
	// so it should never need to get open ports for non-docker containers
	if host.Type != domainproxytypes.HostTypeDocker {
		return nil, errors.New("not implemented")
	}

	return cb.d.getContainerOpenPorts(host.ID)
}

func (d *DockerAgent) startDomainTLSProxy() error {
	domainproxySubnet4Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet4CIDR)
	domainproxySubnet6Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet6CIDR)

	proxy, err := domainproxy.NewDomainTLSProxy(d.host, &DockerProxyCallbacks{d: d})
	if err != nil {
		return fmt.Errorf("create tls domainproxy: %w", err)
	}
	d.domainTLSProxy = proxy

	tproxy, err := bpf.NewTproxy(domainproxySubnet4Prefix, domainproxySubnet6Prefix, []uint16{443})
	if err != nil {
		return fmt.Errorf("tls domainproxy: create tproxy bpf: %w", err)
	}

	err = proxy.Start(netconf.VnetTproxyIP4, netconf.VnetTproxyIP6, domainproxySubnet4Prefix, domainproxySubnet6Prefix, netconf.QueueDomainproxyHttpProbe, tproxy)
	if err != nil {
		return err
	}

	logrus.Debug("started docker tls domainTLSProxy")

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

func (d *DockerAgent) getContainerOpenPorts(containerID string) (map[uint16]struct{}, error) {
	// TODO[6.15pidfd]: save dirfds
	ctr, err := d.realClient.InspectContainer(containerID)
	if err != nil {
		return nil, err
	}
	pid := ctr.State.Pid
	if pid == 0 {
		return map[uint16]struct{}{}, nil
	}

	dfs, err := dirfs.NewFromPath(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return nil, err
	}
	defer dfs.Close()

	openPorts := map[uint16]struct{}{}

	// always grab both v4 and v6 ports because dual stack shows up as ipv6 anyways, so not worth the effort to differentiate
	// especially when our probing routine should be relatively fast anyways, especially for non-listening ports
	listeners4, err := sysnet.ReadProcNetFromDirfs(dfs, "tcp")
	if err != nil {
		return nil, err
	}
	listeners6, err := sysnet.ReadProcNetFromDirfs(dfs, "tcp6")
	if err != nil {
		return nil, err
	}

	for _, listener := range listeners4 {
		openPorts[listener.Port()] = struct{}{}
	}
	for _, listener := range listeners6 {
		openPorts[listener.Port()] = struct{}{}
	}

	return openPorts, nil
}
