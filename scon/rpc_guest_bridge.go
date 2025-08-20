package main

import (
	"fmt"
	"net"
	"net/netip"
	"os"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/nft"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

var (
	vnetPrefix4 = netip.MustParsePrefix(netconf.VnetSubnet4CIDR)
	vnetPrefix6 = netip.MustParsePrefix(netconf.VnetSubnet6CIDR)
)

func prefixIntersects(a, b netip.Prefix) bool {
	// check first IPs
	return a.Contains(b.Masked().Addr()) || b.Contains(a.Masked().Addr())
}

// we only use Linux proxy_arp for IPv4. IPv6 is handled by NDP responder in Swift,
// because Linux NDP proxy can only do individual IPv6 addrs via "ip neigh", not entire subnets
func enableProxyArp(intf string) error {
	err := os.WriteFile("/proc/sys/net/ipv4/conf/"+intf+"/proxy_arp", []byte("1"), 0)
	if err != nil {
		return fmt.Errorf("enable proxy arp: %w", err)
	}

	// set proxy delay to 0
	err = os.WriteFile("/proc/sys/net/ipv4/neigh/"+intf+"/proxy_delay", []byte("0"), 0)
	if err != nil {
		return fmt.Errorf("set proxy delay: %w", err)
	}

	return nil
}

func enableProxyNdp(intf string) error {
	err := os.WriteFile("/proc/sys/net/ipv6/conf/"+intf+"/proxy_ndp", []byte("1"), 0)
	if err != nil {
		return fmt.Errorf("enable proxy arp: %w", err)
	}

	err = os.WriteFile("/proc/sys/net/ipv6/neigh/"+intf+"/proxy_delay", []byte("0"), 0)
	if err != nil {
		return fmt.Errorf("set proxy delay: %w", err)
	}

	// even though we use static ARP proxy entry for this interface, proxy_delay still applies
	err = os.WriteFile("/proc/sys/net/ipv4/neigh/"+intf+"/proxy_delay", []byte("0"), 0)
	if err != nil {
		return fmt.Errorf("set proxy delay: %w", err)
	}

	return nil
}

// this creates a macvlan interface and connects it to Docker bridge via L3 ip forward + proxy ARP/NDP
// that's better than adding it as a bridge member for 2 reasons:
// - works with k8s services, which have no bridge interface (can share the code path)
// - more reliable. for example, no need to deal with 30-sec negative ARP cache if host tried to ping the container before it started
//   - docker container MACs are static for the same IP, so not a big issue in practice
func (s *SconGuestServer) DockerAddBridge(config sgtypes.DockerBridgeConfig, _ *None) (retErr error) {
	// network must not conflict with internal
	if config.IP4Subnet.IsValid() && (prefixIntersects(config.IP4Subnet, vnetPrefix4) || prefixIntersects(config.IP4Subnet, s.m.net.netconf.SconSubnet4)) {
		return fmt.Errorf("subnet %s conflicts with internal OrbStack IPs", config.IP4Subnet)
	}
	if config.IP6Subnet.IsValid() && (prefixIntersects(config.IP6Subnet, vnetPrefix6) || prefixIntersects(config.IP6Subnet, s.m.net.netconf.SconSubnet6)) {
		return fmt.Errorf("subnet %s conflicts with internal OrbStack IPs", config.IP6Subnet)
	}

	// assign vlan ID, create vmnet bridge on host, add to VlanRouter
	vlanId, err := s.m.host.AddDockerBridge(config)
	if err != nil {
		return err
	}
	// cleanup host bridge on failure
	defer func() {
		if retErr != nil {
			_, err := s.m.host.RemoveDockerBridge(config)
			if err != nil {
				logrus.WithError(err).Error("failed to clean up host bridge after failed add")
			}
		}
	}()

	// copy MAC addr template
	// last byte: lower 7 bits are vlan ID, upper 1 bit is 1 (guest)
	// host
	hostMac := make(net.HardwareAddr, len(s.vlanMacTemplate))
	copy(hostMac, s.vlanMacTemplate)
	hostMac[5] = byte(vlanId & 0x7f)
	// guest
	guestMac := make(net.HardwareAddr, len(s.vlanMacTemplate))
	copy(guestMac, s.vlanMacTemplate)
	guestMac[5] = byte(vlanId&0x7f) | 0x80

	// make sure we have a valid pid to attach to for ns
	rt, err := s.dockerMachine.RuntimeState()
	if err != nil {
		return fmt.Errorf("docker machine crashed")
	}

	// create macvlan
	la := netlink.NewLinkAttrs()
	la.Name = fmt.Sprintf("%s%d", agent.DockerBridgeMirrorPrefix, vlanId)
	la.ParentIndex = s.vlanRouterIfi // parent = eth2
	la.MTU = 1500                    // doesn't really matter because GSO, and this is internal-only
	// guest MAC does matter! we use ip forward + proxy arp/ndp, and NDP responder is in Swift, so it needs to know what to reply with
	la.HardwareAddr = guestMac
	// move to container netns (doesn't accept pidfd as nsfd)
	// TODO[6.15pidfd]: stop using pid here
	la.Namespace = netlink.NsPid(rt.InitPid)
	macvlan := &netlink.Macvlan{
		LinkAttrs: la,
		// filter by source MAC
		Mode: netlink.MACVLAN_MODE_SOURCE,
	}
	err = netlink.LinkAdd(macvlan)
	if err != nil {
		return fmt.Errorf("create mirror link: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = netlink.LinkDel(macvlan)
		}
	}()

	// now enter the container's netns... (interface is in there)
	_, err = withContainerNetns(s.dockerMachine, func() (_ struct{}, retErr2 error) {
		defer func() {
			if retErr2 != nil {
				_ = netlink.LinkDel(macvlan)
			}
		}()

		// add host MAC to filter
		err = netlink.MacvlanMACAddrAdd(macvlan, hostMac)
		if err != nil {
			return struct{}{}, fmt.Errorf("add host mac to filter: %w", err)
		}

		// ip forward is already enabled
		// enable proxy arp and ndp
		err = enableProxyArp(macvlan.Name)
		if err != nil {
			return struct{}{}, fmt.Errorf("enable proxy arp: %w", err)
		}

		// routing loop protection
		err = nft.Run("add", "element", "inet", netconf.NftableInet, "host_bridge_ports", fmt.Sprintf("{ %s }", macvlan.Name))
		if err != nil {
			return struct{}{}, fmt.Errorf("add host bridge port to nftables: %w", err)
		}

		// bring it up
		err = netlink.LinkSetUp(macvlan)
		if err != nil {
			return struct{}{}, fmt.Errorf("set mirror link up: %w", err)
		}

		// (k8s has no interface, so this is optional)
		var guestLink netlink.Link
		if config.GuestInterfaceName != "" {
			// get index. don't trust Go API - it does caching
			guestLink, err = netlink.LinkByName(config.GuestInterfaceName)
			if err != nil {
				return struct{}{}, fmt.Errorf("get guest interface: %w", err)
			}

			// enable NDP proxy (required even though we use explicit entries)
			err = enableProxyNdp(config.GuestInterfaceName)
			if err != nil {
				return struct{}{}, fmt.Errorf("enable proxy ndp: %w", err)
			}
		}

		// add routes, but NO addresses, otherwise we create a conflict with the host IP
		// must be done after it's up, or we get "network is down"
		if config.IP4Subnet.IsValid() {
			hostIP := config.HostIP4()
			err = netlink.RouteAdd(&netlink.Route{
				LinkIndex: macvlan.Index,
				Dst: &net.IPNet{
					IP:   hostIP.IP,
					Mask: net.CIDRMask(32, 32),
				},
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("add host ip4 route to mirror link: %w", err)
			}

			if guestLink != nil {
				// ARP proxy for return path
				// to avoid issues, don't enable proxy ARP for everything on the guest interface
				// just add a ARP proxy entry via "ip neigh"
				err = netlink.NeighAdd(&netlink.Neigh{
					LinkIndex: guestLink.Attrs().Index,
					Family:    netlink.FAMILY_V4,
					Flags:     netlink.NTF_PROXY,
					IP:        hostIP.IP,
				})
				if err != nil {
					return struct{}{}, fmt.Errorf("set proxy arp: %w", err)
				}
			}
		}
		if config.IP6Subnet.IsValid() {
			hostIP := config.HostIP6()
			err = netlink.RouteAdd(&netlink.Route{
				LinkIndex: macvlan.Index,
				Dst: &net.IPNet{
					IP:   hostIP.IP,
					Mask: net.CIDRMask(128, 128),
				},
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("add host ip6 route to mirror link: %w", err)
			}

			if guestLink != nil {
				// NDP proxy for return path
				err = netlink.NeighAdd(&netlink.Neigh{
					LinkIndex: guestLink.Attrs().Index,
					Family:    netlink.FAMILY_V6,
					Flags:     netlink.NTF_PROXY,
					IP:        hostIP.IP,
				})
				if err != nil {
					return struct{}{}, fmt.Errorf("set proxy arp: %w", err)
				}
			}
		}

		return struct{}{}, nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *SconGuestServer) DockerRemoveBridge(config sgtypes.DockerBridgeConfig, _ *None) error {
	// remove vmnet bridge on host, remove from VlanRouter, return vlan ID
	vlanId, err := s.m.host.RemoveDockerBridge(config)
	if err != nil {
		return err
	}

	// now enter the container's netns...
	_, err = withContainerNetns(s.dockerMachine, func() (struct{}, error) {
		// delete the link
		ifName := fmt.Sprintf("%s%d", agent.DockerBridgeMirrorPrefix, vlanId)
		err := netlink.LinkDel(&netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: ifName,
			},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("delete mirror link: %w", err)
		}

		// remove routing loop protection
		err = nft.Run("delete", "element", "inet", netconf.NftableInet, "host_bridge_ports", fmt.Sprintf("{ %s }", ifName))
		if err != nil {
			return struct{}{}, fmt.Errorf("delete host bridge port from nftables: %w", err)
		}

		return struct{}{}, nil
	})
	if err != nil {
		return err
	}

	return nil
}
