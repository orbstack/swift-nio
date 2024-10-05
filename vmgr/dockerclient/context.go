package dockerclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orbstack/macvirt/vmgr/conf"
)

type DockerConnection struct {
	Host          string
	SkipTLSVerify bool
	TLSData       *struct {
		CA   string
		Key  string
		Cert string
	}
}

type ContextMetadata struct {
	Name      string
	Endpoints struct {
		Docker DockerConnection
	}
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// https://github.com/docker/cli/blob/30e9abbd3f78d2df1ffd0163fd6eb2a9e4fbbe11/cli/context/store/metadatastore.go#L21
func isContextDir(path string) bool {
	s, err := os.Stat(filepath.Join(path, "meta.json"))
	if err != nil {
		return false
	}
	return !s.IsDir()
}

func GetContext(context string) (*DockerConnection, error) {
	root := filepath.Join(conf.UserDockerDir(), "contexts", "meta")
	fis, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		if fi.IsDir() {
			if !isContextDir(filepath.Join(root, fi.Name())) {
				continue
			}

			metaFile := filepath.Join(root, fi.Name(), "meta.json")
			bytes, err := os.ReadFile(metaFile)

			if err != nil {
				return nil, err
			}

			var metadata ContextMetadata
			if err := json.Unmarshal(bytes, &metadata); err != nil {
				return nil, fmt.Errorf("could not parse context file")
			}

			if metadata.Name == context {
				return &metadata.Endpoints.Docker, nil
			}

		}
	}
	return nil, fmt.Errorf("no such context: %s", context)
}

func GetCurrentContext() (*DockerConnection, error) {
	configFile := filepath.Join(conf.UserDockerDir(), "config.json")
	bytes, err := os.ReadFile(configFile)

	if err != nil {
		return nil, err
	}

	var config struct {
		CurrentContext string `json:"currentContext"`
	}

	if err := json.Unmarshal(bytes, &config); err != nil {
		return nil, fmt.Errorf("could not parse docker config")
	}

	if config.CurrentContext == "" {
		return GetContext("default")
	} else {
		return GetContext(config.CurrentContext)
	}
}
