package vnet

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vnet/bridge"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	brIndexSconMachine = iota
	brIndexVlanRouter

	brUuidSconMachine = "25ef1ee1-1ead-40fd-a97d-f9284917459b"
)

var (
	brMacSconHost           []uint16
	brMacSconGuest          []uint16
	brMacVlanRouterTemplate []uint16

	// 0.0.0.0/8
	zeroNetIPv4 = netip.PrefixFrom(netip.IPv4Unspecified(), 8)
	nat64Subnet = netip.MustParsePrefix(netconf.NAT64Subnet6CIDR)
)

func init() {
	brMacSconHost = mustParseUint16Mac(netconf.HostMACSconBridge)
	brMacSconGuest = mustParseUint16Mac(netconf.GuestMACSconBridge)
	brMacVlanRouterTemplate = mustParseUint16Mac(netconf.VlanRouterMACTemplate)
}

func mustParseUint16Mac(mac string) []uint16 {
	m, err := net.ParseMAC(mac)
	if err != nil {
		panic(err)
	}

	return bytesToUint16(m)
}

func bytesToUint16(b []byte) []uint16 {
	// map to uint16 for json to swift
	// go encodes []uint8 to base64
	macUint16 := make([]uint16, len(b))
	for i, b := range b {
		macUint16[i] = uint16(b)
	}

	return macUint16
}

// Swift does memcmp on this
func slicePrefix6(p netip.Prefix) []uint16 {
	// FIXME: we only do /64 and /96
	if p.Bits()%8 != 0 {
		panic(fmt.Errorf("invalid prefix: %s", p))
	}
	return bytesToUint16(p.Addr().AsSlice()[:p.Bits()/8])
}

func (n *Network) AddHostBridgeFd(fd int) error {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	n.hostBridgeFds = append(n.hostBridgeFds, fd)
	n.hostBridges = append(n.hostBridges, nil)

	if len(n.hostBridgeFds)-1 == brIndexVlanRouter {
		// fds[1] = vlan router
		vlanRouter, err := vzf.SwextNewVlanRouter(vzf.VlanRouterConfig{
			GuestFd:           fd,
			MACPrefix:         brMacVlanRouterTemplate[:5], // prefix bytes
			MaxVlanInterfaces: bridge.MaxVlanInterfaces,
		})
		if err != nil {
			return err
		}

		n.vlanRouter = vlanRouter
		n.vlanIndices = make(map[sgtypes.DockerBridgeConfig]int)
	}

	return nil
}

func (n *Network) ClearVlanBridges(includeScon bool) error {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	// clear first to prevent feedback loop
	logrus.Debug("clearing vlan bridges")
	n.bridgeRouteMon.ClearVlanSubnets()

	err := n.vlanRouter.ClearBridges()
	if err != nil {
		return err
	}

	if includeScon {
		err = n.closeSconMachineHostBridgeLocked()
		if err != nil {
			return err
		}
	}

	n.vlanIndices = make(map[sgtypes.DockerBridgeConfig]int)
	return nil
}

func (n *Network) enableHostBridges() error {
	// create scon machine host bridge
	err := n.CreateSconMachineHostBridge()
	if err != nil {
		return err
	}

	// caller will restart docker machine
	return nil
}

func (n *Network) disableHostBridges() error {
	// clear vlan bridges
	err := n.ClearVlanBridges(true /* includeScon */)
	if err != nil {
		return err
	}

	// caller will restart docker machine
	return nil
}

func (n *Network) MonitorHostBridgeSetting() {
	diffCh := vmconfig.SubscribeDiff()
	for diff := range diffCh {
		if diff.New.NetworkBridge != diff.Old.NetworkBridge {
			logrus.WithFields(logrus.Fields{
				"old": diff.Old.NetworkBridge,
				"new": diff.New.NetworkBridge,
			}).Debug("network bridge setting changed")

			if diff.New.NetworkBridge {
				err := n.enableHostBridges()
				if err != nil {
					logrus.WithError(err).Error("failed to enable host bridges")
				}
			} else {
				err := n.disableHostBridges()
				if err != nil {
					logrus.WithError(err).Error("failed to disable host bridges")
				}
			}
		}
	}
}

func (n *Network) AddVlanBridge(config sgtypes.DockerBridgeConfig) (int, error) {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	// strip interface name so we can use it as a key for ip4/ip6 subnets
	config.GuestInterfaceName = ""

	if _, ok := n.vlanIndices[config]; ok {
		return 0, fmt.Errorf("bridge already exists for config %+v", config)
	}

	if !vmconfig.Get().NetworkBridge {
		return 0, fmt.Errorf("bridges disabled")
	}

	vmnetConfig := vzf.BridgeNetworkConfig{
		GuestFd:         n.hostBridgeFds[brIndexVlanRouter],
		GuestSconFd:     n.hostBridgeFds[brIndexSconMachine],
		ShouldReadGuest: false, // handled by vlan router

		UUID: deriveBridgeConfigUuid(config),
		// this is a template. updated by VlanRouter when it gets index
		HostOverrideMAC: brMacVlanRouterTemplate,
		GuestMAC:        brMacVlanRouterTemplate,
		// doesn't work well
		AllowMulticast: false,

		MaxLinkMTU: int(n.LinkMTU),
	}

	if config.IP4Subnet.IsValid() {
		hostIP := config.HostIP4()
		vmnetConfig.Ip4Address = hostIP.IP.String()
		vmnetConfig.Ip4Mask = net.IP(hostIP.Mask).String()
	}

	if config.IP6Subnet.IsValid() {
		// macOS mask is always /64. we check this on docker side too
		hostIP := config.HostIP6()
		vmnetConfig.Ip6Address = hostIP.IP.String()
		// NDP proxy
		vmnetConfig.NDPReplyPrefix = slicePrefix6(config.IP6Subnet)
	}

	// if addr part of the prefix == 0, then macOS will add RTF_GLOBAL to the routing entry
	// since we skip RTF_GLOBAL, it creates an infinite loop.
	// to be more conservative, exclude all 0.0.0.0/8. they don't work anyway on macOS; only Linux allows them
	if zeroNetIPv4.Contains(config.IP4Subnet.Addr()) {
		return 0, fmt.Errorf("0.0.0.0/8 not allowed on macOS: %s", config.IP4Subnet)
	}
	// for IPv6, just check for all zeros like macOS
	if config.IP6Subnet.Addr().IsUnspecified() {
		return 0, fmt.Errorf("'::' route not allowed on macOS: %s", config.IP6Subnet)
	}

	logrus.WithFields(logrus.Fields{
		"uuid":    vmnetConfig.UUID,
		"ip4":     vmnetConfig.Ip4Address,
		"ip4mask": vmnetConfig.Ip4Mask,
		"ip6":     vmnetConfig.Ip6Address,
		"mac":     vmnetConfig.HostOverrideMAC,
	}).Debug("adding vlan bridge")

	// before actually adding the bridge, let's check for an existing VPN/LAN route.
	// if so, let's not fight with it, just effectively disable our bridge
	hasRoutes, err := bridge.HasAnyValidRoutes(nil, config.IP4Subnet, config.IP6Subnet)
	if err != nil {
		return 0, fmt.Errorf("check routes: %w", err)
	}
	if hasRoutes {
		return 0, errors.New("conflict with existing route")
	}

	index, err := n.vlanRouter.AddBridge(vmnetConfig)
	if err != nil {
		return 0, err
	}

	// monitor route and renew when overridden
	n.bridgeRouteMon.SetSubnet(index, config.IP4Subnet, config.IP6Subnet, func() error {
		n.hostBridgeMu.Lock()
		defer n.hostBridgeMu.Unlock()

		logrus.WithField("config", config).Debug("renewing vlan bridge")
		err := n.vlanRouter.RenewBridge(index)
		if err != nil {
			return err
		}

		return nil
	})

	n.vlanIndices[config] = index
	return index, nil
}

func (n *Network) RemoveVlanBridge(config sgtypes.DockerBridgeConfig) (int, error) {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	// strip interface name so we can use it as a key for ip4/ip6 subnets
	config.GuestInterfaceName = ""

	index, ok := n.vlanIndices[config]
	if !ok {
		return 0, fmt.Errorf("bridge does not exist for config %+v", config)
	}

	n.bridgeRouteMon.ClearSubnet(index)

	logrus.WithField("config", config).Debug("removing vlan bridge")
	err := n.vlanRouter.RemoveBridge(index)
	if err != nil {
		return 0, err
	}

	delete(n.vlanIndices, config)
	return index, nil
}

func deriveBridgeConfigUuid(config sgtypes.DockerBridgeConfig) string {
	// hash the config
	h := sha256.Sum256([]byte(fmt.Sprintf("%+v", config)))
	// uuid format
	uuidBytes := h[:16]
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])
}

func (n *Network) CreateSconMachineHostBridge() error {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	// recreate if needed
	oldBrnet := n.hostBridges[brIndexSconMachine]
	if oldBrnet != nil {
		logrus.Debug("renewing scon machine host bridge")
		oldBrnet.Close()
	} else {
		logrus.Debug("creating scon machine host bridge")

		// first time, so add to route monitor - either after adding (if OK), or after error (if not OK)
		// if sucessful, then we don't want to add it until creation done, to avoid feedback loop
		prefix4 := netip.MustParsePrefix(netconf.SconSubnet4CIDR)
		prefix6 := netip.MustParsePrefix(netconf.SconSubnet6CIDR)
		defer n.bridgeRouteMon.SetSubnet(bridge.IndexSconMachine, prefix4, prefix6, func() error {
			return n.CreateSconMachineHostBridge()
		})

		// if this is the first time, check if there's an existing VPN or LAN route.
		// if so, let's not fight with it, just effectively disable our bridge
		hasRoutes, err := bridge.HasAnyValidRoutes(nil, prefix4, prefix6)
		if err != nil {
			return fmt.Errorf("check routes: %w", err)
		}
		if hasRoutes {
			// there's a conflict. can we get away with v6 only?
			hasRoutes, err := bridge.HasAnyValidRoutes(nil, netip.Prefix{}, prefix6)
			if err != nil {
				return fmt.Errorf("check routes (v6): %w", err)
			}
			if hasRoutes {
				return errors.New("conflict with existing route")
			}

			// v4 conflicts but v6 doesn't. let's just use v6
			// as persistent flag to avoid breaking stuff later if they restart VPN
			// usually this is only Surge/v2ray anyway
			n.disableMachineBridgeV4 = true
		}

		// we still register the subnet monitor via defer,
		// so we'll try again later if the VPN is turned off
	}

	config := vzf.BridgeNetworkConfig{
		GuestFd:         n.hostBridgeFds[brIndexSconMachine],
		GuestSconFd:     n.hostBridgeFds[brIndexSconMachine],
		ShouldReadGuest: true,

		UUID:       brUuidSconMachine,
		Ip4Address: netconf.SconHostBridgeIP4,
		Ip4Mask:    netconf.SconSubnet4Mask,
		Ip6Address: netconf.SconHostBridgeIP6,

		HostOverrideMAC: brMacSconHost,
		// scon machine bridge doesn't use ip forward/proxy arp - it bridges machines directly
		// so this is just the VM's MAC for NDP responder use
		GuestMAC:       brMacSconGuest,
		NDPReplyPrefix: slicePrefix6(nat64Subnet),
		// allow all multicast, not just mDNs
		AllowMulticast: true,

		MaxLinkMTU: int(n.LinkMTU),
	}
	if n.disableMachineBridgeV4 {
		config.Ip4Address = ""
		config.Ip4Mask = ""
	}

	err := n.createHostBridge(brIndexSconMachine, config)
	if err != nil {
		return err
	}

	return nil
}

func (n *Network) closeSconMachineHostBridgeLocked() error {
	brnet := n.hostBridges[brIndexSconMachine]
	if brnet == nil {
		return nil
	}

	// remove from route monitor
	n.bridgeRouteMon.ClearSubnet(bridge.IndexSconMachine)

	logrus.Debug("closing scon machine host bridge")
	err := brnet.Close()
	if err != nil {
		return err
	}

	n.hostBridges[brIndexSconMachine] = nil
	return nil
}

func (n *Network) createHostBridge(index int, config vzf.BridgeNetworkConfig) error {
	brnet, err := vzf.SwextNewBrnet(config)
	if err != nil {
		return err
	}

	n.hostBridges[index] = brnet
	return nil
}

// This recreates the bridge if the route to the machine subnet is wrong.
// Usually happens when VPN is enabled (changes to utun3) or when VPN is disabled (route is deleted, changes to en0).
// Since we don't have root, recreating the bridge is necessary to fix the route.
//
// Works because TCP flows, etc. don't get terminated. Just a brief ~100-200 ms of packet loss
// -----------------------------------------
// monitor route changes to relevant subnets
func (n *Network) MonitorHostBridgeRoutes() error {
	return n.bridgeRouteMon.Monitor()
}

func (n *Network) stopHostBridges() {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	n.bridgeRouteMon.Close()

	for _, b := range n.hostBridges {
		if b == nil {
			continue
		}

		logrus.WithField("bridge", b).Debug("closing bridge")
		err := b.Close()
		if err != nil {
			logrus.WithError(err).WithField("bridge", b).Warn("failed to close bridge")
		}
	}

	n.hostBridges = nil

	n.vlanRouter.Close()
	n.vlanRouter = nil

	// close fds
	for _, fd := range n.hostBridgeFds {
		unix.Close(fd)
	}
}
