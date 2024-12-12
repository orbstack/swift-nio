package swext

import (
	"github.com/orbstack/macvirt/vmgr/vnet/vnettypes"
)

type vzfMonitor struct{}

var VzfMonitor = &vzfMonitor{}

func (m vzfMonitor) NetworkMTU() int {
	// our kernel no longer has double vnet hdr hacks for forcing TSO
	return vnettypes.BaseMTU
}

func (m vzfMonitor) NetworkWantsVnetHdrV1() bool {
	return false
}
