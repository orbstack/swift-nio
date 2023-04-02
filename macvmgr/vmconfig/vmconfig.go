package vmconfig

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"runtime"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mem"
)

const (
	defaultMemoryLimit = 8 * 1024 * 1024 * 1024 // 8 GiB
)

var (
	globalConfig   *VmConfig
	globalConfigMu sync.Mutex
)

type VmConfig struct {
	MemoryMiB    uint64 `json:"memory_mib"`
	CPU          int    `json:"cpu"`
	Rosetta      bool   `json:"rosetta"`
	NetworkProxy string `json:"network_proxy"`
}

type VmConfigPatch struct {
	MemoryMiB    *uint64 `json:"memory_mib,omitempty"`
	CPU          *int    `json:"cpu,omitempty"`
	Rosetta      *bool   `json:"rosetta,omitempty"`
	NetworkProxy *string `json:"network_proxy,omitempty"`
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
	if c.NetworkProxy != "" {
		u, err := url.Parse(c.NetworkProxy)
		if err != nil {
			return err
		}

		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return errors.New("unsupported proxy protocol. supported protocols: http, https, socks5, socks5h")
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

	data, err := os.ReadFile(conf.VmConfigFile())
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
	config := Get()

	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()

	cb(config)

	err := config.Validate()
	if err != nil {
		return err
	}

	// generate a patch and only save the patch
	// this allows us to change defaults without breaking existing configs
	diff := Diff(Defaults(), config)

	data, err := json.MarshalIndent(diff, "", "\t")
	if err != nil {
		return err
	}

	// apfs doesn't need to be synced
	err = os.WriteFile(conf.VmConfigFile(), data, 0644)
	if err != nil {
		return err
	}

	globalConfig = config
	return nil
}

func calcMemory() uint64 {
	hostMem := mem.PhysicalMemory()
	targetMem := hostMem / 3
	if targetMem > defaultMemoryLimit {
		return defaultMemoryLimit
	}
	return targetMem
}

func Defaults() *VmConfig {
	return &VmConfig{
		MemoryMiB:    calcMemory() / 1024 / 1024,
		CPU:          runtime.NumCPU(),
		Rosetta:      true,
		NetworkProxy: "",
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
}
