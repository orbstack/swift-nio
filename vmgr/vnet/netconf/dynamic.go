package netconf

import (
	"fmt"
	"net/netip"
)

type Config struct {
	// 192.168.138.0/23
	MergedSubnet4 netip.Prefix

	// 192.168.138.0/24
	DomainproxySubnet4 netip.Prefix
	// fd07:b51a:cc66:0:cafe::/112
	DomainproxySubnet6 netip.Prefix

	// 192.168.139.0/24
	SconSubnet4 netip.Prefix
	// .1
	SconGatewayIP4  netip.Addr
	SconWebIndexIP4 netip.Addr
	// .2
	SconDockerIP4 netip.Addr
	SconK8sIP4    netip.Addr
	// .3
	SconHostBridgeIP4 netip.Addr
	// .10
	SconDHCPStartIP4 netip.Addr
	// .247
	SconDHCPEndIP4 netip.Addr

	// fd07:b5a:cc66:0::/64
	// :0 is canonical format, not :0000
	SconSubnet6 netip.Prefix
	// :1
	SconGatewayIP6  netip.Addr
	SconWebIndexIP6 netip.Addr
	// :2
	SconDockerIP6 netip.Addr
	// = :2 (docker)
	SconK8sIP6 netip.Addr
	// = NAT64SourceIP6, to make NAT64 easier
	SconHostBridgeIP6 netip.Addr
}

func ip4WithOffset(ip netip.Addr, off0, off1, off2, off3 uint8) netip.Addr {
	bits := ip.As4()
	bits[0] += off0
	bits[1] += off1
	bits[2] += off2
	bits[3] += off3
	return netip.AddrFrom4(bits)
}

func ConfigFromVmconfigSubnet(configSubnet4 netip.Prefix) (*Config, error) {
	cfg := &Config{}

	// we slice it such that the first /24 is domainproxy and the second /24 is scon/machines
	if !configSubnet4.Addr().Is4() {
		return nil, fmt.Errorf("subnet '%s' is not an IPv4 prefix", configSubnet4)
	}
	if configSubnet4.Bits() != 23 {
		return nil, fmt.Errorf("subnet '%s' is not a /23 prefix", configSubnet4)
	}
	cfg.MergedSubnet4 = configSubnet4.Masked()

	var err error
	cfg.DomainproxySubnet4, err = cfg.MergedSubnet4.Addr().Prefix(24)
	if err != nil {
		return nil, fmt.Errorf("slice prefix: %w", err)
	}

	// for the second subnet, do bit math
	cfg.SconSubnet4 = netip.PrefixFrom(ip4WithOffset(cfg.MergedSubnet4.Addr(), 0, 0, 1, 0), 24)

	// fill in the rest
	cfg.SconGatewayIP4 = cfg.SconSubnet4.Addr().Next()
	cfg.SconWebIndexIP4 = cfg.SconGatewayIP4
	cfg.SconDockerIP4 = cfg.SconGatewayIP4.Next()
	cfg.SconK8sIP4 = cfg.SconDockerIP4
	cfg.SconHostBridgeIP4 = cfg.SconK8sIP4.Next()
	cfg.SconDHCPStartIP4 = ip4WithOffset(cfg.SconSubnet4.Addr(), 0, 0, 0, DHCPLeaseStart)
	cfg.SconDHCPEndIP4 = ip4WithOffset(cfg.SconSubnet4.Addr(), 0, 0, 0, DHCPLeaseEnd)

	// ipv6 is fixed for now
	cfg.DomainproxySubnet6 = netip.MustParsePrefix(DefaultDomainproxySubnet6CIDR)
	cfg.SconSubnet6 = netip.MustParsePrefix(DefaultSconSubnet6CIDR)
	cfg.SconGatewayIP6 = cfg.SconSubnet6.Addr().Next()
	cfg.SconWebIndexIP6 = cfg.SconGatewayIP6
	cfg.SconDockerIP6 = cfg.SconGatewayIP6.Next()
	cfg.SconK8sIP6 = cfg.SconDockerIP6
	cfg.SconHostBridgeIP6 = netip.MustParseAddr(NAT64SourceIP6)

	return cfg, nil
}
