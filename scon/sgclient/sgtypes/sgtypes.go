package sgtypes

import (
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

type DockerContainersDiff struct {
	Added   []dockertypes.ContainerSummaryMin
	Removed []dockertypes.ContainerSummaryMin
}
