package agent

import (
	"errors"
	"fmt"
	"net/netip"
	"os"

	"github.com/orbstack/macvirt/scon/domainproxy"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/util"
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

func (cb *DockerProxyCallbacks) NfqueueMarkReject(mark uint32) uint32 {
	return netconf.DockerFwmarkNfqueueReject
}

func (cb *DockerProxyCallbacks) NfqueueMarkSkip(mark uint32) uint32 {
	return netconf.DockerFwmarkNfqueueSkip
}

func (cb *DockerProxyCallbacks) NftableName() string {
	return netconf.NftableInet
}

func (cb *DockerProxyCallbacks) GetMachineOpenPorts(machineID string) (map[uint16]struct{}, error) {
	// docker machine should never need to get a machine's ports: machine domains are handled by ovm
	return nil, errors.New("not implemented")
}

func (cb *DockerProxyCallbacks) GetContainerOpenPorts(containerID string) (map[uint16]struct{}, error) {
	return cb.d.getDockerContainerOpenPorts(containerID)
}

func (d *DockerAgent) startDomainTLSProxy() error {
	domainproxySubnet4Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet4CIDR)
	domainproxySubnet6Prefix := netip.MustParsePrefix(netconf.DomainproxySubnet6CIDR)

	proxy, err := domainproxy.NewDomainTLSProxy(d.host, &DockerProxyCallbacks{d: d})
	if err != nil {
		return fmt.Errorf("create tls domainproxy: %w", err)
	}
	d.domainTLSProxy = proxy

	err = proxy.Start(netconf.VnetTproxyIP4, netconf.VnetTproxyIP6, domainproxySubnet4Prefix, domainproxySubnet6Prefix, netconf.QueueDomainproxyProbe, netconf.QueueDomainproxyProbeGso)
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

func (d *DockerAgent) getDockerContainerOpenPorts(containerID string) (map[uint16]struct{}, error) {
	ctr, err := d.client.InspectContainer(containerID)
	if err != nil {
		return nil, err
	}
	pid := ctr.State.Pid
	if pid == 0 {
		logrus.Debugf("getDockerContainerOpenPorts: no pid for container %v", containerID)
		return map[uint16]struct{}{}, nil
	}

	logrus.Debugf("getDockerContainerOpenPorts: got pid %d for container %v", pid, containerID)

	openPorts := map[uint16]struct{}{}

	// always grab both v4 and v6 ports because dual stack shows up as ipv6 anyways, so not worth the effort to differentiate
	// especially when our probing routine should be relatively fast anyways, especially for non-listening ports
	netTcp4, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp", pid))
	if err != nil {
		return nil, err
	}

	netTcp6, err := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp6", pid))
	if err != nil {
		return nil, err
	}

	err = util.ParseNetTcpPorts(string(netTcp4), openPorts)
	if err != nil {
		return nil, err
	}

	err = util.ParseNetTcpPorts(string(netTcp6), openPorts)
	if err != nil {
		return nil, err
	}

	return openPorts, nil
}

func (a *AgentServer) DockerGetContainerOpenPorts(containerID string, reply *map[uint16]struct{}) error {
	openPorts, err := a.docker.getDockerContainerOpenPorts(containerID)
	if err != nil {
		return err
	}

	*reply = openPorts
	return nil
}
