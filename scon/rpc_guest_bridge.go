package main

import (
	"fmt"
	"net"
	"os"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

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
	initPid := s.dockerMachine.initPid
	if initPid == -1 {
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
	la.Namespace = netlink.NsPid(initPid)
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

	// add iptables rule to block FORWARD to this subnet
	// prevents routing loop if host pings a non-existent k8s service ip, and docker machine tries to fulfill
	// we *could* instead add a DROP rule to FORWARD in the docker machine, effectively binding it so that it only forwards to one interface. but since k8s has no interface, that's not possible. this solution allows sharing the code path
	// we could *also* block outgoing conns to this on the host side, using BridgeRouteMon, but that's racy: vmnet doesn't return until it succeeds, at which point interface is already up and we're too late. if we optimistically block it too early, then it could disrupt traffic on user's conflicting subnets. it's also far more complicated wrt. renewal when conflicting subnets appear/disappear on the host.
	if config.IP4Subnet.IsValid() {
		err = s.m.net.BlockIptablesForward(config.IP4Subnet)
		if err != nil {
			return fmt.Errorf("block iptables forward: %w", err)
		}
		defer func() {
			if retErr != nil {
				_ = s.m.net.UnblockIptablesForward(config.IP4Subnet)
			}
		}()
	}
	if config.IP6Subnet.IsValid() {
		err = s.m.net.BlockIptablesForward(config.IP6Subnet)
		if err != nil {
			return fmt.Errorf("block iptables forward: %w", err)
		}
		defer func() {
			if retErr != nil {
				_ = s.m.net.UnblockIptablesForward(config.IP6Subnet)
			}
		}()
	}

	// now enter the container's netns... (interface is in there)
	_, err = withContainerNetns(s.dockerMachine, func() (_ struct{}, retErr2 error) {
		defer func() {
			if retErr2 != nil {
				_ = netlink.LinkDel(macvlan)
			}
		}()

		err = netlink.LinkSetAllmulticastOff(macvlan)
		if err != nil {
			return struct{}{}, fmt.Errorf("set mirror link flags: %w", err)
		}

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

	// add host ip to cfwd bpf
	// TODO: fix potential race if host connects after interface is up, but before this
	s.dockerMachine.mu.RLock()
	defer s.dockerMachine.mu.RUnlock()
	if s.dockerMachine.bpf != nil {
		if config.IP4Subnet.IsValid() {
			hostIP := config.HostIP4().IP
			err := s.dockerMachine.bpf.CfwdAddHostIP(hostIP)
			if err != nil {
				return fmt.Errorf("add host ip %+v to cfwd: %w", hostIP, err)
			}
		}
		if config.IP6Subnet.IsValid() {
			hostIP := config.HostIP6().IP
			err := s.dockerMachine.bpf.CfwdAddHostIP(hostIP)
			if err != nil {
				return fmt.Errorf("add host ip %+v to cfwd: %w", hostIP, err)
			}
		}
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
		err := netlink.LinkDel(&netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: fmt.Sprintf("%s%d", agent.DockerBridgeMirrorPrefix, vlanId),
			},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("delete mirror link: %w", err)
		}

		return struct{}{}, nil
	})
	if err != nil {
		return err
	}

	// unblock forwarding in case this conflicts with user's networks
	if config.IP4Subnet.IsValid() {
		err = s.m.net.UnblockIptablesForward(config.IP4Subnet)
		if err != nil {
			return fmt.Errorf("unblock iptables forward: %w", err)
		}
	}
	if config.IP6Subnet.IsValid() {
		err = s.m.net.UnblockIptablesForward(config.IP6Subnet)
		if err != nil {
			return fmt.Errorf("unblock iptables forward: %w", err)
		}
	}

	// remove host ip from cfwd bpf
	s.dockerMachine.mu.RLock()
	defer s.dockerMachine.mu.RUnlock()
	if s.dockerMachine.bpf != nil {
		if config.IP4Subnet.IsValid() {
			hostIP := config.HostIP4().IP
			err := s.dockerMachine.bpf.CfwdRemoveHostIP(hostIP)
			if err != nil {
				return fmt.Errorf("remove host ip %+v from cfwd: %w", hostIP, err)
			}
		}
		if config.IP6Subnet.IsValid() {
			hostIP := config.HostIP6().IP
			err := s.dockerMachine.bpf.CfwdRemoveHostIP(hostIP)
			if err != nil {
				return fmt.Errorf("remove host ip %+v from cfwd: %w", hostIP, err)
			}
		}
	}

	return nil
}
