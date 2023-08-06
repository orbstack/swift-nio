package sgtypes

import (
	"net"
	"net/netip"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

type DockerBridgeConfig struct {
	// for host
	IP4Subnet  netip.Prefix
	IP4Gateway netip.Addr // for checking bip/lastIP conflict
	IP6Subnet  netip.Prefix

	// for scon
	GuestInterfaceName string
}

func (config *DockerBridgeConfig) HostIP4() (net.IP, net.IPMask) {
	mask := prefixToMask(config.IP4Subnet)
	ip := net.IP(config.IP4Subnet.Addr().AsSlice())
	// last IP - to avoid conflict with containers or gateway
	ip = lastIPInSubnet(ip, mask)
	// if it conflicts with the Linux-side host/gateway IP (bip), subtract 1 more from last octet
	if ip.Equal(config.IP4Gateway.AsSlice()) {
		ip[len(ip)-1]--
	}

	return ip, mask
}

func (config *DockerBridgeConfig) HostIP6() (net.IP, net.IPMask) {
	mask := prefixToMask(config.IP6Subnet)
	ip := net.IP(config.IP6Subnet.Addr().AsSlice())
	// last IP - to avoid conflict with containers or gateway
	// this has basically no chance of conflicting with a user-selected or SLAAC IP,
	// and Docker doesn't provide gateway IP for, so we don't check for v6 bip conflict
	ip = lastIPInSubnet(ip, mask)

	return ip, mask
}

type DockerContainersDiff struct {
	Added   []dockertypes.ContainerSummaryMin
	Removed []dockertypes.ContainerSummaryMin
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
