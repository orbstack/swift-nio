package agent

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/orbstack/macvirt/scon/nftables"
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

const (
	NfsetBridgeSubnets4 = "docker_bridge_subnets4"
	NfsetBridgeHostIps4 = "docker_bridge_host_ips4"
	NfsetBridgeSubnets6 = "docker_bridge_subnets6"
	NfsetBridgeHostIps6 = "docker_bridge_host_ips6"
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

	// skip DOCKER-ISOLATION-STAGE-1 chain to allow cross-bridge traffic
	// jump from FORWARD gets restored on every bridge creation, and STAGE-2 jumps are inserted, so we have to delete and reinsert
	// can't use nftables to override:
	//   - accept = continue across tables; drop = immediate stop
	//   - can't make ours last (prio+1): that only works within chains
	//   - can't add a chain to iptables-nft's table: it complains
	_ = util.Run("iptables", "-D", "DOCKER-ISOLATION-STAGE-1", "-j", "RETURN")
	err := util.Run("iptables", "-I", "DOCKER-ISOLATION-STAGE-1", "-j", "RETURN")
	if err != nil {
		logrus.WithError(err).Warn("failed to add iptables rule")
	}
	_ = util.Run("ip6tables", "-D", "DOCKER-ISOLATION-STAGE-1", "-j", "RETURN")
	err = util.Run("ip6tables", "-I", "DOCKER-ISOLATION-STAGE-1", "-j", "RETURN")
	// won't exist if no ipv6
	if err != nil && !strings.Contains(err.Error(), "No chain/target/match by that name") {
		logrus.WithError(err).Warn("failed to add iptables rule")
	}

	var newNetworks []dockertypes.Network
	err = d.client.Call("GET", "/networks", nil, &newNetworks)
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
	removed, added := util.DiffSlicesKey(d.lastNetworks, newNetworks)
	slices.SortStableFunc(removed, compareNetworks)
	slices.SortStableFunc(added, compareNetworks)

	// remove first
	// must remove before adding in case of recreate with same name within debounce period
	for _, n := range removed {
		err = d.onNetworkRemove(n)
		if err != nil {
			logrus.WithError(err).Error("failed to remove network")
		}
	}

	// then add
	for _, n := range added {
		err = d.onNetworkAdd(n)
		if err != nil {
			logrus.WithError(err).Error("failed to add network")
		}
	}

	dockerBridgeIfaces := make([]string, 0, len(newNetworks)+1)
	for _, n := range newNetworks {
		if n.Driver != "bridge" || n.Scope != "local" {
			continue
		}

		dockerBridgeIfaces = append(dockerBridgeIfaces, dockerNetworkToInterfaceName(&n))
	}
	dockerBridgeIfaces = append(dockerBridgeIfaces, "eth0")

	// update flowtable
	err = nftables.Run("add", "flowtable", "inet", "orbstack", "ft", fmt.Sprintf("{ hook ingress priority filter; devices = { %s }; }", strings.Join(dockerBridgeIfaces, ", ")))
	if err != nil {
		return fmt.Errorf("update nft interfaces: %w", err)
	}

	d.lastNetworks = newNetworks
	return nil
}

func dockerNetworkToInterfaceName(n *dockertypes.Network) string {
	if name, ok := n.Options["com.docker.network.bridge.name"]; ok {
		// covers docker0, docker_gwbridge cases
		return name
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
	var ip6Gateway netip.Addr
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
			ip6Gateway, err = netip.ParseAddr(ipam.Gateway)
			if err != nil {
				// default = first addr in subnet
				// get the zero addr (masked), then add one
				ip6Gateway = subnet.Masked().Addr().Next()
			}
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
		IP6Gateway:         ip6Gateway,
		GuestInterfaceName: ifName,
	}, true
}

func editNftablesSet(action, setName, element string) error {
	return nftables.Run(action, "element", "inet", "orbstack", setName, "{ "+element+" }")
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

	err = editNftablesSet("add", "docker_bridges", dockerNetworkToInterfaceName(&network))
	if err != nil {
		return err
	}

	// add host and gateway IPs to nfsets
	if config.IP4Subnet.IsValid() {
		err = editNftablesSet("add", NfsetBridgeSubnets4, config.IP4Subnet.String())
		if err != nil {
			logrus.WithError(err).WithField("net", config.IP4Subnet).Error("failed to add bridge net to set")
		}

		hostIP := config.HostIP4().IP
		err = editNftablesSet("add", NfsetBridgeHostIps4, hostIP.String())
		if err != nil {
			logrus.WithError(err).WithField("ip", hostIP).Error("failed to add host ip to set")
		}
	}
	if config.IP6Subnet.IsValid() {
		err = editNftablesSet("add", NfsetBridgeSubnets6, config.IP6Subnet.String())
		if err != nil {
			logrus.WithError(err).WithField("net", config.IP6Subnet).Error("failed to add bridge net to set")
		}

		hostIP := config.HostIP6().IP
		err = editNftablesSet("add", NfsetBridgeHostIps6, hostIP.String())
		if err != nil {
			logrus.WithError(err).WithField("ip", hostIP).Error("failed to add host ip to set")
		}
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

	err = editNftablesSet("delete", "docker_bridges", dockerNetworkToInterfaceName(&network))
	if err != nil {
		return err
	}

	// remove host and gateway IPs from nfsets
	if config.IP4Subnet.IsValid() {
		err = editNftablesSet("delete", NfsetBridgeSubnets4, config.IP4Subnet.String())
		if err != nil {
			logrus.WithError(err).WithField("ip", config.IP4Subnet).Error("failed to remove bridge net from set")
		}

		hostIP := config.HostIP4().IP
		err = editNftablesSet("delete", NfsetBridgeHostIps4, hostIP.String())
		if err != nil {
			logrus.WithError(err).WithField("ip", hostIP).Error("failed to remove gateway ip from set")
		}
	}
	if config.IP6Subnet.IsValid() {
		err = editNftablesSet("delete", NfsetBridgeSubnets6, config.IP6Subnet.String())
		if err != nil {
			logrus.WithError(err).WithField("ip", config.IP6Subnet).Error("failed to remove bridge net from set")
		}

		hostIP := config.HostIP6().IP
		err = editNftablesSet("delete", NfsetBridgeHostIps6, hostIP.String())
		if err != nil {
			logrus.WithError(err).WithField("ip", hostIP).Error("failed to remove gateway ip from set")
		}
	}

	return nil
}
