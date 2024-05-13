package vzf

import (
	"github.com/orbstack/macvirt/vmgr/vnet/vnettypes"
)

type monitor struct{}

var Monitor = &monitor{}

func (m monitor) NetworkMTU() int {
	// our kernel no longer has double vnet hdr hacks for forcing TSO
	return vnettypes.BaseMTU
}

func (m monitor) NetworkWantsVnetHdr() bool {
	return false
}
