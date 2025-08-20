package netutil

import (
	"net"
	"net/netip"
)

func AddrToPrefix(addr netip.Addr, prefix netip.Prefix) netip.Prefix {
	return netip.PrefixFrom(addr, prefix.Bits())
}

func PrefixToIPNet(prefix netip.Prefix) *net.IPNet {
	return &net.IPNet{
		IP:   net.IP(prefix.Addr().AsSlice()),
		Mask: PrefixToIPMask(prefix),
	}
}

func AddrToIPNet(addr netip.Addr, prefix netip.Prefix) *net.IPNet {
	return PrefixToIPNet(netip.PrefixFrom(addr, prefix.Bits()))
}

func PrefixToIPMask(prefix netip.Prefix) net.IPMask {
	return net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())
}

func PrefixToNetmaskIP(prefix netip.Prefix) netip.Addr {
	addr, ok := netip.AddrFromSlice(PrefixToIPMask(prefix))
	if !ok {
		panic("failed to convert IPMask to IP")
	}
	return addr
}
