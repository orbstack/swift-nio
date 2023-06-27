package vmconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/conf/mem"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
)

const (
	minMemoryMib     = 500                    // 500 MiB
	maxDefaultMemory = 8 * 1024 * 1024 * 1024 // 8 GiB

	ProxyNone = "none"
	ProxyAuto = "auto"
)

var (
	globalConfig   *VmConfig
	globalConfigMu sync.Mutex

	diffBroadcaster = syncx.NewBroadcaster[VmConfigChange]()
)

type VmConfig struct {
	MemoryMiB       uint64 `json:"memory_mib"`
	CPU             int    `json:"cpu"`
	Rosetta         bool   `json:"rosetta"`
	NetworkProxy    string `json:"network_proxy"`
	NetworkBridge   bool   `json:"network_bridge"`
	MountHideShared bool   `json:"mount_hide_shared"`
	DataDir         string `json:"data_dir,omitempty"`
}

type VmConfigChange struct {
	Old *VmConfig `json:"old"`
	New *VmConfig `json:"new"`
}

func (c *VmConfig) Validate() error {
	if c.MemoryMiB < minMemoryMib {
		return fmt.Errorf("memory must be at least %d MiB", minMemoryMib)
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

	if c.DataDir != "" {
		err := os.MkdirAll(c.DataDir, 0755)
		if err != nil {
			// is this an external drive? if so, can we stat it?
			if errors.Is(err, os.ErrPermission) && strings.HasPrefix(c.DataDir, "/Volumes/") {
				volumeDir := strings.Split(c.DataDir, "/")[2]
				if _, err := os.Stat("/Volumes/" + volumeDir); err != nil {
					return fmt.Errorf("external data drive is not accessible: %w", err)
				}
			}

			return fmt.Errorf("create data dir: %w", err)
		}

		err = validateAPFS(c.DataDir)
		if err != nil {
			return fmt.Errorf("validate data dir: %w", err)
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
	if err != nil {
		logrus.WithError(err).Fatal("Invalid config")
	}

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
	diffDefault, err := diffJsonMaps(Defaults(), &newConfig)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(diffDefault, "", "\t")
	if err != nil {
		return err
	}

	// apfs doesn't need to be synced
	err = os.WriteFile(coredir.VmConfigFile(), data, 0644)
	if err != nil {
		return err
	}

	// broadcast the change from old, if anything changed
	if newConfig != *oldConfig {
		diffBroadcaster.Emit(VmConfigChange{
			Old: oldConfig,
			New: &newConfig,
		})
	}

	globalConfig = &newConfig
	return nil
}

func diffJsonMaps(a, b any) (map[string]any, error) {
	aJson, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}

	bJson, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}

	var aMap, bMap map[string]any
	err = json.Unmarshal(aJson, &aMap)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(bJson, &bMap)
	if err != nil {
		return nil, err
	}

	diff := make(map[string]any)
	for k, v := range bMap {
		if aMap[k] != v {
			diff[k] = v
		}
	}

	return diff, nil
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
		NetworkBridge:   true,
		MountHideShared: false,
		DataDir:         "",
	}
}

func Reset() error {
	return Update(func(c *VmConfig) {
		*c = *Defaults()
	})
}

func SubscribeDiff() chan VmConfigChange {
	return diffBroadcaster.Subscribe()
}

func UnsubscribeDiff(ch chan VmConfigChange) {
	diffBroadcaster.Unsubscribe(ch)
}
