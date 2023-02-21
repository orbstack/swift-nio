package vmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
)

const (
	CurrentVersion = 1
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
		return fmt.Errorf("vmgr state version %d is newer than current version %d", c.Version, CurrentVersion)
	}

	if c.Version < 0 {
		return fmt.Errorf("vmgr state version %d is invalid", c.Version)
	}

	if c.Arch != runtime.GOARCH {
		return fmt.Errorf("vmgr state architecture %s is different from current architecture %s", c.Arch, runtime.GOARCH)
	}

	return nil
}

func GetState() *VmgrState {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	if globalState != nil {
		return globalState
	}

	data, err := os.ReadFile(conf.VmStateFile())
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

func UpdateState(cb func(*VmgrState)) error {
	state := GetState()

	globalStateMu.Lock()
	defer globalStateMu.Unlock()

	cb(state)

	err := state.Validate()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "\t")
	if err != nil {
		return err
	}

	// apfs doesn't need to be synced
	err = os.WriteFile(conf.VmStateFile(), data, 0644)
	if err != nil {
		return err
	}

	globalState = state
	return nil
}

func defaultState() *VmgrState {
	return &VmgrState{
		Version: CurrentVersion,
		Arch:    runtime.GOARCH,
	}
}
