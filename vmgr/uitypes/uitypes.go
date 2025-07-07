package uitypes

import (
	"time"

	stypes "github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
)

// now that we use leading debounce, this is fast enough
const UIEventDebounce = 100 * time.Millisecond

type UIEvent struct {
	Vmgr       *VmgrEvent       `json:"vmgr"`
	Scon       *SconEvent       `json:"scon"`
	Docker     *DockerEvent     `json:"docker"`
	DrmWarning *DrmWarningEvent `json:"drmWarning"`
	// workaround to avoid importing huge k8s pkg in scon and agent
	K8s any `json:"k8s"`
}

type VmgrEvent struct {
	NewDaemonPid *int `json:"newDaemonPid"`
	StateReady   bool `json:"stateReady"`
	// also to avoid importing vmconfig pkg
	VmConfig any       `json:"vmConfig"`
	DrmState *DrmState `json:"drmState"`
}

type SconEvent struct {
	CurrentMachines []stypes.ContainerInfo `json:"currentMachines"`
}

type DockerEntity int

const (
	DockerEntityContainer DockerEntity = iota
	DockerEntityVolume
	DockerEntityImage
	DockerEntityNetwork

	DockerEntityMax_
)

type DockerEvent struct {
	CurrentContainers []*dockertypes.ContainerSummary `json:"currentContainers"`
	CurrentVolumes    []*dockertypes.Volume           `json:"currentVolumes"`
	CurrentImages     []dockertypes.Image             `json:"currentImages"`
	CurrentNetworks   []dockertypes.Network           `json:"currentNetworks"`
	CurrentSystemDf   *dockertypes.SystemDf           `json:"currentSystemDf"`

	Exited *ExitEvent `json:"exited"`
}

type DrmWarningEvent struct {
	LastError string `json:"lastError"`
}

type ExitEvent struct {
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
}

type DrmState struct {
	RefreshToken       string                      `json:"refreshToken,omitempty"`
	EntitlementTier    drmtypes.EntitlementTier    `json:"entitlementTier"`
	EntitlementType    drmtypes.EntitlementType    `json:"entitlementType"`
	EntitlementMessage *string                     `json:"entitlementMessage,omitempty"`
	EntitlementStatus  *drmtypes.EntitlementStatus `json:"entitlementStatus"`
}
