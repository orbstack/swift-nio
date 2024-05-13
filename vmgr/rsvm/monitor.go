package rsvm

import (
	"github.com/orbstack/macvirt/vmgr/vnet/vnettypes"
)

type monitor struct{}

var Monitor = &monitor{}

func (m monitor) NetworkMTU() int {
	return vnettypes.PreferredMTU
}

func (m monitor) NetworkWantsVnetHdrV1() bool {
	return true
}
