package sgtypes

import "net/netip"

type DockerBridgeConfig struct {
	// for host
	IP4Subnet netip.Prefix
	IP6Subnet netip.Prefix

	// for scon
	GuestInterfaceName string
}
