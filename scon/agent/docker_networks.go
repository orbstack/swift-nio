package agent

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

const (
	dockerDefaultBridgeNetwork = "bridge"
	DockerBridgeMirrorPrefix   = ".orbmirror"
)

func compareNetworks(a, b dockertypes.Network) int {
	// always rank default bridge network first
	if a.Name == dockerDefaultBridgeNetwork {
		return -1
	} else if b.Name == dockerDefaultBridgeNetwork {
		return 1
	}

	return cmp.Compare(a.Name, b.Name)
}

func findLink(links []netlink.Link, name string) netlink.Link {
	for _, l := range links {
		if l.Attrs().Name == name {
			return l
		}
	}
	return nil
}

func (d *DockerAgent) filterNewNetworks(nets []dockertypes.Network) ([]dockertypes.Network, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}

	var newNets []dockertypes.Network
	for _, n := range nets {
		// we only deal with local + bridge
		if n.Driver != "bridge" || n.Scope != "local" {
			continue
		}

		// must have 1+ active containers
		// but only full network obj has Containers and that's kinda expensive, so check interface membership instead
		ifName := dockerNetworkToInterfaceName(&n)

		// find index of bridge
		bridgeLink := findLink(links, ifName)
		if bridgeLink == nil {
			logrus.WithField("network", n.Name).Error("bridge not found")
			continue
		}
		bridgeIndex := bridgeLink.Attrs().Index

		// check if there are any containers (veth) attached
		hasMembers := false
		for _, l := range links {
			attrs := l.Attrs()
			if attrs.MasterIndex == bridgeIndex && !strings.HasPrefix(attrs.Name, DockerBridgeMirrorPrefix) {
				hasMembers = true
				break
			}
		}
		if !hasMembers {
			continue
		}

		newNets = append(newNets, n)
	}
	return newNets, nil
}

func (d *DockerAgent) refreshNetworks() error {
	// no mu needed: FuncDebounce has mutex

	var newNetworks []dockertypes.Network
	err := d.client.Call("GET", "/networks", nil, &newNetworks)
	if err != nil {
		return err
	}

	// filter out networks with no active containers.
	// people can have a lot of old compose networks piled up, causing us to reach vmnet bridge limit
	newNetworks, err = d.filterNewNetworks(newNetworks)
	if err != nil {
		return err
	}

	// diff
	added, removed := util.DiffSlicesKey[string](d.lastNetworks, newNetworks)
	slices.SortStableFunc(added, compareNetworks)
	slices.SortStableFunc(removed, compareNetworks)

	// add first
	for _, n := range added {
		err = d.onNetworkAdd(n)
		if err != nil {
			logrus.WithError(err).Error("failed to add network")
		}
	}

	// then remove
	for _, n := range removed {
		err = d.onNetworkRemove(n)
		if err != nil {
			logrus.WithError(err).Error("failed to remove network")
		}
	}

	d.lastNetworks = newNetworks
	return nil
}

func dockerNetworkToInterfaceName(n *dockertypes.Network) string {
	if n.Name == "bridge" {
		return "docker0"
	} else {
		return "br-" + n.ID[:12]
	}
}

func dockerNetworkToBridgeConfig(n dockertypes.Network) (sgtypes.DockerBridgeConfig, bool) {
	// requirements:
	//   - ipv4, ipv6, or 4+6
	//   - ipv6 must be /64
	//   - max 1 of each network type
	//   - min 1 type
	var ip4Subnet netip.Prefix
	var ip4Gateway netip.Addr
	var ip6Subnet netip.Prefix
	for _, ipam := range n.IPAM.Config {
		subnet, err := netip.ParsePrefix(ipam.Subnet)
		if err != nil {
			logrus.WithField("subnet", ipam.Subnet).Warn("failed to parse network subnet")
			continue
		}

		if subnet.Addr().Is4() {
			if ip4Subnet.IsValid() {
				// duplicate v4 - not supported, could break
				return sgtypes.DockerBridgeConfig{}, false
			}

			ip4Subnet = subnet
			ip4Gateway, err = netip.ParseAddr(ipam.Gateway)
			if err != nil {
				// default = first addr in subnet
				// get the zero addr (masked), then add one
				ip4Gateway = subnet.Masked().Addr().Next()
			}
		} else if n.EnableIPv6 {
			// ignore v6 if not enabled
			if ip6Subnet.IsValid() {
				// duplicate v6 - not supported, could break
				return sgtypes.DockerBridgeConfig{}, false
			}

			// must be /64 - macOS doesn't support other prefix lens for vmnet
			if subnet.Bits() != 64 {
				// if not, then skip v6 - we may still be able to use v4
				continue
			}

			ip6Subnet = subnet
		}
	}

	// must have at least one
	if !ip4Subnet.IsValid() && !ip6Subnet.IsValid() {
		return sgtypes.DockerBridgeConfig{}, false
	}

	// resolve interface name
	ifName := dockerNetworkToInterfaceName(&n)

	return sgtypes.DockerBridgeConfig{
		IP4Subnet:          ip4Subnet,
		IP4Gateway:         ip4Gateway,
		IP6Subnet:          ip6Subnet,
		GuestInterfaceName: ifName,
	}, true
}

func (d *DockerAgent) onNetworkAdd(network dockertypes.Network) error {
	config, ok := dockerNetworkToBridgeConfig(network)
	if !ok {
		logrus.WithField("name", network.Name).Debug("ignoring network")
		return nil
	}

	logrus.WithField("name", network.Name).WithField("config", config).Info("adding network")
	err := d.scon.DockerAddBridge(config)
	if err != nil {
		return err
	}

	return nil
}

func (d *DockerAgent) onNetworkRemove(network dockertypes.Network) error {
	// this works because we have the full Network object from lastNetworks diff
	config, ok := dockerNetworkToBridgeConfig(network)
	if !ok {
		return nil
	}

	logrus.WithField("name", network.Name).WithField("config", config).Info("removing network")
	err := d.scon.DockerRemoveBridge(config)
	if err != nil {
		return err
	}

	return nil
}
