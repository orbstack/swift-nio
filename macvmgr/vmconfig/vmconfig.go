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
	cachedConfig *VmConfig
	globalMu     sync.RWMutex
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
	globalMu.RLock()
	defer globalMu.RUnlock()

	if cachedConfig != nil {
		return cachedConfig
	}

	data, err := os.ReadFile(conf.VmConfigFile())
	check(err)

	config := Defaults()
	err = json.Unmarshal(data, &config)
	check(err)

	err = config.Validate()
	check(err)

	cachedConfig = config
	return cachedConfig
}

func Update(func(*VmConfig)) error {
	globalMu.Lock()
	defer globalMu.Unlock()

	config := Get()

	func(config *VmConfig) {
		err := config.Validate()
		if err != nil {
			panic(err)
		}
	}(config)

	data, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return err
	}

	// apfs doesn't need to be synced
	err = os.WriteFile(conf.VmConfigFile(), data, 0644)
	if err != nil {
		return err
	}

	cachedConfig = config
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
