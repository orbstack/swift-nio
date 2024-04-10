package vmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
)

const (
	CurrentVersion = 3
)

var (
	globalState   *VmgrState
	globalStateMu sync.Mutex
)

type VmgrState struct {
	Version int    `json:"version"`
	Arch    string `json:"arch"`
}

func (c *VmgrState) Validate() error {
	if c.Version > CurrentVersion {
		return fmt.Errorf("last-used version %d is newer than current version %d. Downgrades are not supported; please update OrbStack", c.Version, CurrentVersion)
	}

	if c.Version < 0 {
		return fmt.Errorf("invalid vmgr state version %d", c.Version)
	}

	// we allow migrating between architectures thanks to emulation
	if c.Arch != "arm64" && c.Arch != "amd64" {
		return fmt.Errorf("invalid vmgr state arch %q", c.Arch)
	}

	return nil
}

func GetState() *VmgrState {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	if globalState != nil {
		return globalState
	}

	data, err := os.ReadFile(coredir.VmStateFile())
	if err != nil {
		if os.IsNotExist(err) {
			return defaultState()
		}
		panic(err)
	}

	state := defaultState()
	err = json.Unmarshal(data, &state)
	check(err)

	err = state.Validate()
	check(err)

	globalState = state
	return globalState
}

func UpdateState(cb func(*VmgrState) error) error {
	oldState := GetState()
	// copy for mutating
	newState := *oldState

	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	err := cb(&newState)
	if err != nil {
		return err
	}

	err = newState.Validate()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(&newState, "", "\t")
	if err != nil {
		return err
	}

	// apfs doesn't need to be synced
	err = os.WriteFile(coredir.VmStateFile(), data, 0644)
	if err != nil {
		return err
	}

	globalState = &newState
	return nil
}

func defaultState() *VmgrState {
	return &VmgrState{
		Version: CurrentVersion,
		Arch:    runtime.GOARCH,
	}
}
