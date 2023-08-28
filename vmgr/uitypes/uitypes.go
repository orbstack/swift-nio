package uitypes

import (
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	v1 "k8s.io/api/core/v1"
)

type UIEvent struct {
	Started    *StartedEvent        `json:"started"`
	Docker     *dockertypes.UIEvent `json:"docker"`
	DrmWarning *DrmWarningEvent     `json:"drmWarning"`
	K8s        *K8sEvent            `json:"k8s"`
}

type StartedEvent struct {
	Pid int `json:"pid"`
}

type DrmWarningEvent struct {
	LastError string `json:"lastError"`
}

type K8sEvent struct {
	CurrentPods     []*v1.Pod     `json:"currentPods"`
	CurrentServices []*v1.Service `json:"currentServices"`
}
