package vmconfig

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mem"
)

const (
	defaultMemoryLimit = 8 * 1024 * 1024 * 1024 // 8 GiB
)

var (
	globalConfig   *VmConfig
	globalConfigMu sync.RWMutex
)

type VmConfig struct {
	MemoryMiB uint64 `json:"memory_mib"`
}

func (c *VmConfig) Validate() error {
	err := c.validatePlatform()
	if err != nil {
		return err
	}

	return nil
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func Get() *VmConfig {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()

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

	data, err := json.MarshalIndent(config, "", "\t")
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
	if hostMem > defaultMemoryLimit {
		return defaultMemoryLimit
	}
	return hostMem / 3
}

func Defaults() *VmConfig {
	return &VmConfig{
		MemoryMiB: calcMemory() / 1024 / 1024,
	}
}

func Reset() error {
	return Update(func(c *VmConfig) {
		*c = *Defaults()
	})
}
