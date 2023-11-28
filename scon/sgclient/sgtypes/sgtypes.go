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
	IP6Gateway netip.Addr // ipv6 should not conflict, but this is used for tls proxy ipset

	// for scon
	GuestInterfaceName string
}

func (config *DockerBridgeConfig) HostIP4() net.IPNet {
	mask := prefixToMask(config.IP4Subnet)

	// first IP (x.y.z.0) to avoid conflict with containers or gateway
	// this is the best option because:
	//   - Docker gateway is normally .1, and Docker never seems to assign .0
	//   - K8s will never assign .0: https://github.com/kubernetes/kubernetes/blob/fb785f1f42183e26e2b9f042474391c4d58433bb/pkg/registry/core/service/ipallocator/bitmap.go#L81
	//   - historically .0 was broadcast but not for a *long* time, so it's safe to use unlike 0.0.0.x
	ip := net.IP(config.IP4Subnet.Masked().Addr().AsSlice())

	// only chance of conflict is if user manually assigns .0 as gateway,
	// so we check for that and use last IP (x.y.z.254) instead. (255 is broadcast)
	if ip.Equal(config.IP4Gateway.AsSlice()) {
		ip = lastIPInSubnet(ip, mask)
	}

	return net.IPNet{
		IP:   ip,
		Mask: mask,
	}
}

func (config *DockerBridgeConfig) HostIP6() net.IPNet {
	// last IP - to avoid conflict with containers or gateway
	// for some reason, first IP (zero IP) makes Linux use loopback route for v6
	mask := prefixToMask(config.IP6Subnet)
	ip := net.IP(config.IP6Subnet.Addr().AsSlice())
	ip = lastIPInSubnet(ip, mask)

	// unlike v4 this has basically no chance of conflicting with a user-selected or SLAAC IP,
	// and Docker doesn't provide gateway IP for v6, so we don't check for v6 bip conflict
	return net.IPNet{
		IP:   ip,
		Mask: mask,
	}
}

type Diff[T any] struct {
	Removed []T
	Added   []T
}

type TaggedImage struct {
	Tag   string
	Image *dockertypes.FullImage
}

func (t *TaggedImage) Identifier() string {
	// we do need to diff by ID to catch changed (rebuilt) images with same tag
	return t.Image.ID
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
