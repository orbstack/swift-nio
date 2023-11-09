package dockerconf

import (
	"encoding/json"
	"os"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/sirupsen/logrus"
)

func FixDockerCredsStore() error {
	dockerConfigPath := conf.UserDockerDir() + "/config.json"
	if _, err := os.Stat(dockerConfigPath); err == nil {
		// read the file
		data, err := os.ReadFile(dockerConfigPath)
		if err != nil {
			return err
		}

		// parse it
		var config map[string]interface{}
		err = json.Unmarshal(data, &config)
		if err != nil {
			return err
		}

		// check if it's set to desktop
		if config["credsStore"] == "desktop" {
			// regardless of whether docker-credential-desktop exists, change it to osxkeychain.
			// because "desktop" doesn't work without dialing desktop socket
			logrus.Debug("docker-credential-desktop doesn't exist, changing credsStore to osxkeychain")
			// change it to osxkeychain
			config["credsStore"] = "osxkeychain"

			// write it back
			data, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return err
			}
			err = os.WriteFile(dockerConfigPath, data, 0644)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func DockerDesktopDaemonConfig() string {
	return conf.UserDockerDir() + "/daemon.json"
}
