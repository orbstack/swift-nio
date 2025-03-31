package vmconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/conf/mem"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/util/errorx"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
)

const (
	minMemoryMib     = 500                     // 500 MiB
	maxDefaultMemory = 16 * 1024 * 1024 * 1024 // 16 GiB

	ProxyNone = "none"
	ProxyAuto = "auto"
)

var (
	globalConfig   *vmtypes.VmConfig
	globalConfigMu syncx.Mutex

	diffBroadcaster = syncx.NewBroadcaster[VmConfigChange]()
)

type VmConfigChange struct {
	Old *vmtypes.VmConfig `json:"old"`
	New *vmtypes.VmConfig `json:"new"`
}

func Validate(c *vmtypes.VmConfig) error {
	if c.MemoryMiB < minMemoryMib {
		return fmt.Errorf("memory must be at least %d MiB", minMemoryMib)
	}

	// idk if this is user error or an old GUI bug, but fix MemoryMiB being set as bytes:
	// max reasonable memory is 1.5
	if c.MemoryMiB/1024/1024 >= 2 {
		// wrong unit. fix it
		c.MemoryMiB = c.MemoryMiB / 1024 / 1024
	}

	// clamp cpus
	c.CPU = min(c.CPU, runtime.NumCPU())
	c.CPU = max(c.CPU, 1)

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

	if !types.ContainerNameRegex.MatchString(c.DockerNodeName) {
		return fmt.Errorf("invalid docker node name: %s", c.DockerNodeName)
	}

	return nil
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func Get() *vmtypes.VmConfig {
	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()

	if globalConfig != nil {
		return globalConfig
	}

	defaults, err := Defaults()
	if err != nil {
		panic(err)
	}

	data, err := os.ReadFile(coredir.VmConfigFile())
	if err != nil {
		if os.IsNotExist(err) {
			return defaults
		}
		panic(err)
	}

	config := defaults
	err = json.Unmarshal(data, config)
	if err != nil {
		return defaults
	}

	// apply debug overlay on debug builds
	if coredir.Debug() {
		data, err := os.ReadFile(coredir.VmConfigFileDebug())
		if err == nil {
			err = json.Unmarshal(data, &config)
			check(err)
		}
	}

	// apply test overlay on test builds
	if coredir.TestMode() {
		// default: fresh test data dir
		config.DataDir = coredir.AppDir() + "/data.test"
		_ = os.RemoveAll(config.DataDir)

		data, err := os.ReadFile(coredir.VmConfigFileTest())
		if err == nil {
			err = json.Unmarshal(data, &config)
			check(err)
		}
	}

	err = Validate(config)
	if err != nil {
		errorx.Fatalf("Invalid config: %w", err)
	}

	globalConfig = config
	return globalConfig
}

func Update(cb func(*vmtypes.VmConfig)) error {
	oldConfig := Get()

	globalConfigMu.Lock()
	defer globalConfigMu.Unlock()

	// make a copy for mutating
	newConfig := *oldConfig
	cb(&newConfig)

	err := Validate(&newConfig)
	if err != nil {
		return err
	}

	// generate a patch and only save the patch
	// this allows us to change defaults without breaking existing configs
	defaults, err := Defaults()
	if err != nil {
		return fmt.Errorf("get defaults: %w", err)
	}
	diffDefault, err := diffJsonMaps(defaults, &newConfig)
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
		diffBroadcaster.EmitQueued(VmConfigChange{
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
	targetMem := hostMem / 2
	return min(targetMem, maxDefaultMemory)
}

func BaseDefaults() *vmtypes.VmConfig {
	return &vmtypes.VmConfig{
		MemoryMiB:          calcMemory() / 1024 / 1024,
		CPU:                runtime.NumCPU(),
		Rosetta:            runtime.GOARCH == "arm64",
		NetworkProxy:       ProxyAuto,
		NetworkBridge:      true,
		NetworkHttps:       true,
		MountHideShared:    false,
		DataDir:            "",
		DataAllowBackup:    false,
		DockerSetContext:   true,
		DockerNodeName:     "orbstack",
		SetupUseAdmin:      IsAdmin(),
		K8sEnable:          false,
		K8sExposeServices:  false,
		SSHExposePort:      false,
		Power_PauseOnSleep: true,
	}
}

func Reset() error {
	return Update(func(c *vmtypes.VmConfig) {
		defaults, err := Defaults()
		if err != nil {
			panic(err)
		}

		*c = *defaults
	})
}

func SubscribeDiff() chan VmConfigChange {
	return diffBroadcaster.Subscribe()
}

func UnsubscribeDiff(ch chan VmConfigChange) {
	diffBroadcaster.Unsubscribe(ch)
}
