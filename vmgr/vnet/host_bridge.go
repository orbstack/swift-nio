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
	brMacSconMachine        []uint16
	brMacVlanRouterTemplate []uint16

	zeroNetIPv4 = netip.MustParsePrefix("0.0.0.0/8")
)

func init() {
	brMacSconMachine = mustParseUint16Mac(netconf.SconHostBridgeMAC)
	brMacVlanRouterTemplate = mustParseUint16Mac(netconf.VlanRouterMACTemplate)
}

func mustParseUint16Mac(mac string) []uint16 {
	m, err := net.ParseMAC(mac)
	if err != nil {
		panic(err)
	}

	// map to uint16 for json to swift
	macUint16 := make([]uint16, len(m))
	for i, b := range m {
		macUint16[i] = uint16(b)
	}

	return macUint16
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
		ShouldReadGuest: false, // handled by vlan router

		UUID: deriveBridgeConfigUuid(config),
		// this is a template. updated by VlanRouter when it gets index
		HostOverrideMAC: brMacVlanRouterTemplate,
		// doesn't work well
		AllowMulticast: false,

		MaxLinkMTU: int(n.LinkMTU),
	}

	if config.IP4Subnet.IsValid() {
		mask := prefixToMask(config.IP4Subnet)
		ip := net.IP(config.IP4Subnet.Addr().AsSlice())
		// last IP - to avoid conflict with containers or gateway
		ip = lastIPInSubnet(ip, mask)
		// if it conflicts with the Linux-side host/gateway IP (bip), subtract 1 more from last octet
		if ip.Equal(config.IP4Gateway.AsSlice()) {
			ip[len(ip)-1]--
		}
		vmnetConfig.Ip4Address = ip.String()
		vmnetConfig.Ip4Mask = net.IP(mask).String()
	}

	if config.IP6Subnet.IsValid() {
		mask := prefixToMask(config.IP6Subnet)
		ip := net.IP(config.IP6Subnet.Addr().AsSlice())
		// last IP - to avoid conflict with containers or gateway
		// this has basically no chance of conflicting with a user-selected or SLAAC IP,
		// and Docker doesn't provide gateway IP for, so we don't check for v6 bip conflict
		ip = lastIPInSubnet(ip, mask)
		vmnetConfig.Ip6Address = ip.String()
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
		return n.vlanRouter.RenewBridge(index)
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

func prefixToMask(prefix netip.Prefix) net.IPMask {
	nBits := prefix.Bits()
	mask := make(net.IPMask, len(prefix.Addr().AsSlice()))
	for i := 0; i < len(mask); i++ {
		if nBits >= 8 {
			mask[i] = 0xff
			nBits -= 8
		} else if nBits > 0 {
			mask[i] = byte(0xff << (8 - nBits))
			nBits = 0
		} else {
			mask[i] = 0
		}
	}
	return mask
}

// last IP in range
func lastIPInSubnet(addr net.IP, mask net.IPMask) net.IP {
	// copy
	addr = append([]byte(nil), addr...)

	// apply mask
	for i := range addr {
		addr[i] |= ^mask[i]
	}

	// subtract 1 from last octet to avoid broadcast
	addr[len(addr)-1]--

	return addr
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
			return errors.New("conflict with existing route")
		}

		// we still register the subnet monitor via defer,
		// so we'll try again later if the VPN is turned off
	}

	err := n.createHostBridge(brIndexSconMachine, vzf.BridgeNetworkConfig{
		GuestFd:         n.hostBridgeFds[brIndexSconMachine],
		ShouldReadGuest: true,

		UUID:       brUuidSconMachine,
		Ip4Address: netconf.SconHostBridgeIP4,
		Ip4Mask:    netconf.SconSubnet4Mask,
		Ip6Address: netconf.SconHostBridgeIP6,

		HostOverrideMAC: brMacSconMachine,
		// for .local names
		AllowMulticast: true,

		MaxLinkMTU: int(n.LinkMTU),
	})
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
