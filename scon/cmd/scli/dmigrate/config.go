package dmigrate

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/sirupsen/logrus"
)

const (
	refConfigDockerDesktop = `
	{
		"builder": { "gc": { "defaultKeepStorage": "20GB", "enabled": true } },
		"experimental": false,
		"features": { "buildkit": true }
	}
	`
)

func diffAddedChangedRecursive(old, new map[string]any) map[string]any {
	d := make(map[string]any)
	// only check keys in new
	// ignore deletions. maybe people replaced it with an empty config
	for k, v := range new {
		if _, ok := old[k]; !ok {
			d[k] = v
			continue
		}

		// obj changed - compare recursively
		if newMap, ok := v.(map[string]any); ok {
			var oldMap map[string]any
			if _oldMap, ok := old[k].(map[string]any); ok {
				oldMap = _oldMap
			} else {
				// old is not a map. compare against empty map
				oldMap = make(map[string]any)
			}

			// recurse
			diff := diffAddedChangedRecursive(oldMap, newMap)
			if len(diff) > 0 {
				d[k] = diff
			}
			continue
		}

		// same - compare val
		if old[k] == v {
			continue
		}

		// val changed
		d[k] = v
	}

	return d
}

func (m *Migrator) migrateDaemonConfig(path string) error {
	defer m.finishOneEntity()

	// read src config
	var srcConfig map[string]any
	srcConfigData, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read src config: %w", err)
	}
	err = json.Unmarshal(srcConfigData, &srcConfig)
	if err != nil {
		return fmt.Errorf("unmarshal src config: %w", err)
	}

	// decode ref config
	var refConfig map[string]any
	err = json.Unmarshal([]byte(refConfigDockerDesktop), &refConfig)
	if err != nil {
		return fmt.Errorf("unmarshal ref config: %w", err)
	}

	// diff: if any keys added or changed, recursively
	// we only want to apply the difference and keep our defaults
	diff := diffAddedChangedRecursive(refConfig, srcConfig)
	if len(diff) == 0 {
		return nil
	}

	logrus.Infof("Migrating daemon config: %v", diff)

	// user changed it. write out the new config
	newConfigData, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal new config: %w", err)
	}
	err = os.WriteFile(conf.DockerDaemonConfig(), newConfigData, 0644)
	if err != nil {
		return fmt.Errorf("write new config: %w", err)
	}

	// restart docker machine
	c, err := scli.Client().GetByID(types.ContainerIDDocker)
	if err != nil {
		return fmt.Errorf("get docker machine: %w", err)
	}
	err = scli.Client().ContainerRestart(c)
	if err != nil {
		return fmt.Errorf("restart docker machine: %w", err)
	}

	return nil
}
