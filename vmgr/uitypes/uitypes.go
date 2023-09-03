package uitypes

import (
	"time"

	stypes "github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

const UIEventDebounce = 50 * time.Millisecond

type UIEvent struct {
	Vmgr       *VmgrEvent           `json:"vmgr"`
	Scon       *SconEvent           `json:"scon"`
	Docker     *dockertypes.UIEvent `json:"docker"`
	DrmWarning *DrmWarningEvent     `json:"drmWarning"`
	// workaround to avoid importing huge k8s pkg in scon and agent
	K8s any `json:"k8s"`
}

type VmgrEvent struct {
	NewDaemonPid *int `json:"newDaemonPid"`
	StateReady   bool `json:"stateReady"`
	// also to avoid importing vmconfig pkg
	VmConfig any `json:"vmConfig"`
}

type SconEvent struct {
	CurrentMachines []stypes.ContainerRecord `json:"currentMachines"`
}

type DrmWarningEvent struct {
	LastError string `json:"lastError"`
}
