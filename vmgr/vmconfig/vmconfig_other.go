//go:build !darwin

package vmconfig

import (
	"sync"

	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
)

func validateAPFS(dataDir string) error {
	return nil
}

var IsAdmin = sync.OnceValue(func() bool {
	return false
})

func Defaults() (*vmtypes.VmConfig, error) {
	return BaseDefaults(), nil
}
