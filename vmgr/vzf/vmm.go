package vzf

import (
	"github.com/orbstack/macvirt/vmgr/osver"
	"github.com/orbstack/macvirt/vmgr/vnet/vnettypes"
)

type monitor struct{}

var Monitor = &monitor{}

func (m monitor) NetworkMTU() int {
	if osver.IsAtLeast("v13.0") {
		return vnettypes.PreferredMTU
	} else {
		return vnettypes.BaseMTU
	}
}
