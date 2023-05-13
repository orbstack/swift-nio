package vmconfig

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"runtime"
	"sync"

	"github.com/orbstack/macvirt/macvmgr/conf/coredir"
	"github.com/orbstack/macvirt/macvmgr/conf/mem"
	"github.com/orbstack/macvirt/macvmgr/syncx"
)

const (
	maxDefaultMemory = 8 * 1024 * 1024 * 1024 // 8 GiB

	ProxyNone = "none"
	ProxyAuto = "auto"
)

var (
	globalConfig   *VmConfig
	globalConfigMu sync.Mutex

	diffBroadcaster = syncx.NewBroadcaster[VmConfigPatch]()
)

type VmConfig struct {
	MemoryMiB       uint64 `json:"memory_mib"`
	CPU             int    `json:"cpu"`
	Rosetta         bool   `json:"rosetta"`
	NetworkProxy    string `json:"network_proxy"`
	MountHideShared bool   `json:"mount_hide_shared"`
	DataDir         string `json:"data_dir"`
}

type VmConfigPatch struct {
	MemoryMiB       *uint64 `json:"memory_mib,omitempty"`
	CPU             *int    `json:"cpu,omitempty"`
	Rosetta         *bool   `json:"rosetta,omitempty"`
	NetworkProxy    *string `json:"network_proxy,omitempty"`
	MountHideShared *bool   `json:"mount_hide_shared,omitempty"`
	DataDir         *string `json:"data_dir,omitempty"`
}

func (c *VmConfig) Validate() error {
	err := c.validatePlatform()
	if err != nil {
		return err
	}

	// clamp cpus
	if c.CPU < 1 {
		c.CPU = 1
	}
	if c.CPU > runtime.NumCPU() {
		c.CPU = runtime.NumCPU()
	}

	// must be a supported proxy protocol
	if c.NetworkProxy != ProxyNone && c.NetworkProxy != ProxyAuto {
		u, err := url.Parse(c.NetworkProxy)
		if err != nil {
			return err
		}

		if u.Host == "" {
			return errors.New("invalid proxy URL")
		}
		if u.Path != "" && u.Path != "/" {
			return errors.New("proxy URL must not contain a path")
		}

		switch u.Scheme {
		case "http", "https", "socks5":
		default:
			return errors.New("invalid proxy. supported: 'auto', 'none', or protocols: http, https, socks5")
		}
	}

	return nil
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func Get() *VmConfig {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()

	if globalConfig != nil {
		return globalConfig
	}

	data, err := os.ReadFile(coredir.VmConfigFile())
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults()
		}
		panic(err)
	}

	config := Defaults()
	err = json.Unmarshal(data, &config)
	check(err)

	err = config.Validate()
	check(err)

	globalConfig = config
	return globalConfig
}

func Update(cb func(*VmConfig)) error {
	oldConfig := Get()

	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()

	// make a copy for mutating
	newConfig := *oldConfig
	cb(&newConfig)

	err := newConfig.Validate()
	if err != nil {
		return err
	}

	// generate a patch and only save the patch
	// this allows us to change defaults without breaking existing configs
	diffDefault := Diff(Defaults(), &newConfig)

	data, err := json.MarshalIndent(diffDefault, "", "\t")
	if err != nil {
		return err
	}

	// apfs doesn't need to be synced
	err = os.WriteFile(coredir.VmConfigFile(), data, 0644)
	if err != nil {
		return err
	}

	// broadcast the diff from old, if anything changed
	if newConfig != *oldConfig {
		diffOld := Diff(oldConfig, &newConfig)
		diffBroadcaster.Emit(*diffOld)
	}

	globalConfig = &newConfig
	return nil
}

func calcMemory() uint64 {
	hostMem := mem.PhysicalMemory()
	targetMem := hostMem / 3
	if targetMem > maxDefaultMemory {
		return maxDefaultMemory
	}
	return targetMem
}

func Defaults() *VmConfig {
	return &VmConfig{
		MemoryMiB:       calcMemory() / 1024 / 1024,
		CPU:             runtime.NumCPU(),
		Rosetta:         true,
		NetworkProxy:    ProxyAuto,
		MountHideShared: false,
	}
}

func Reset() error {
	return Update(func(c *VmConfig) {
		*c = *Defaults()
	})
}

func Diff(a, b *VmConfig) *VmConfigPatch {
	patch := &VmConfigPatch{}

	if a.MemoryMiB != b.MemoryMiB {
		patch.MemoryMiB = &b.MemoryMiB
	}

	if a.CPU != b.CPU {
		patch.CPU = &b.CPU
	}

	if a.Rosetta != b.Rosetta {
		patch.Rosetta = &b.Rosetta
	}

	if a.NetworkProxy != b.NetworkProxy {
		patch.NetworkProxy = &b.NetworkProxy
	}

	if a.MountHideShared != b.MountHideShared {
		patch.MountHideShared = &b.MountHideShared
	}

	return patch
}

func Apply(a *VmConfig, patch *VmConfigPatch) {
	if patch.MemoryMiB != nil {
		a.MemoryMiB = *patch.MemoryMiB
	}

	if patch.CPU != nil {
		a.CPU = *patch.CPU
	}

	if patch.Rosetta != nil {
		a.Rosetta = *patch.Rosetta
	}

	if patch.NetworkProxy != nil {
		a.NetworkProxy = *patch.NetworkProxy
	}

	if patch.MountHideShared != nil {
		a.MountHideShared = *patch.MountHideShared
	}
}

func SubscribeDiff() chan VmConfigPatch {
	return diffBroadcaster.Subscribe()
}

func UnsubscribeDiff(ch chan VmConfigPatch) {
	diffBroadcaster.Unsubscribe(ch)
}
