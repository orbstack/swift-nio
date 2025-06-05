package cliutil

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/vmclient"
)

func GetSyntheticVmConfig() (map[string]any, error) {
	config, err := vmclient.Client().GetConfig()
	if err != nil {
		return nil, fmt.Errorf("get config: %w", err)
	}

	// serialize to json
	jsonData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	// deserialize to map
	var configMap map[string]any
	err = json.Unmarshal(jsonData, &configMap)
	if err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// add synthetic configs
	guiExe, err := conf.FindGUIExe()
	if err != nil {
		return nil, fmt.Errorf("find gui exe: %w", err)
	}
	out, err := util.RunWithOutput(guiExe, "get-launch-at-login")
	if err != nil {
		return nil, fmt.Errorf("run gui exe: %w", err)
	}
	configMap["app.start_at_login"] = strings.TrimSpace(out) == "true"

	// add machine configs
	containers, err := scli.Client().ListContainers()
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	for _, container := range containers {
		namePart := container.Record.Name
		if strings.ContainsRune(namePart, '.') {
			namePart = "\"" + namePart + "\""
		}
		configMap["machine."+namePart+".username"] = container.Record.Config.DefaultUsername
	}

	return configMap, nil
}
