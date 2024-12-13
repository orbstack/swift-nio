package vmconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/syncx"
)

const (
	// major = 0, minor = 1-3 matches (0, legacy version)
	CurrentMajorVersion = 0
	CurrentMinorVersion = 4

	LastLegacyVersion = 2
)

var (
	globalState   *VmgrState
	globalStateMu syncx.Mutex
)

type VmgrSetupState struct {
	PathUpdateRequested bool     `json:"pathUpdateRequested"`
	EditedShellProfiles []string `json:"editedShellProfiles"`
	SshEdited           bool     `json:"sshEdited"`
}

type VmgrState struct {
	LegacyVersion uint           `json:"version,omitempty"`
	MajorVersion  uint           `json:"majorVersion"`
	MinorVersion  uint           `json:"minorVersion"`
	Arch          string         `json:"arch"`
	SetupState    VmgrSetupState `json:"setupState"`
}

func (c *VmgrState) Validate() error {
	if c.MajorVersion > CurrentMajorVersion {
		return fmt.Errorf("last-used major version %d is newer than current major version %d. Downgrades are not supported; please update OrbStack", c.MajorVersion, CurrentMajorVersion)
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

	// always write state, to set current version on init
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
		MajorVersion: CurrentMajorVersion,
		MinorVersion: CurrentMinorVersion,
		Arch:         runtime.GOARCH,
		SetupState: VmgrSetupState{
			EditedShellProfiles: []string{},
			SshEdited:           false,
		},
	}
}
