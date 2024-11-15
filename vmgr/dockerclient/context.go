package dockerclient

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orbstack/macvirt/vmgr/conf"
)

type TLSData struct {
	CA   string
	Key  string
	Cert string
}

type DockerConnection struct {
	Host          string
	SkipTLSVerify bool
	TLSData       *TLSData
}

type ContextMetadata struct {
	Name      string
	Endpoints struct {
		Docker DockerConnection
	}
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

const defaultContextName = "default"
const defaultHost = "unix:///var/run/docker.sock"
const defaultTLSHost = "tcp://localhost:2376"

func resolveDefaultDockerDaemon() (*DockerConnection, error) {
	daemon := DockerConnection{SkipTLSVerify: false}

	if path := os.Getenv("DOCKER_CERT_PATH"); path != "" {
		daemon.TLSData = &TLSData{
			CA:   filepath.Join(path, "ca.pem"),
			Key:  filepath.Join(path, "key.pem"),
			Cert: filepath.Join(path, "cert.pem"),
		}
	}

	if hostOverride := os.Getenv("DOCKER_HOST"); hostOverride != "" {
		daemon.Host = hostOverride
	} else if daemon.TLSData != nil {
		daemon.Host = defaultTLSHost
	} else {
		daemon.Host = defaultHost
	}

	switch tlsVerify := os.Getenv("DOCKER_TLS_VERIFY"); tlsVerify {
	case "1":
		daemon.SkipTLSVerify = false
	case "0":
		daemon.SkipTLSVerify = true
	}

	return &daemon, nil
}

func resolveDockerDaemon(context string) (*DockerConnection, error) {
	// if a context is explicitly specified, read the corresponding meta.json file
	root := filepath.Join(conf.UserDockerDir(), "contexts", "meta")
	contextDir := fmt.Sprintf("%x", sha256.Sum256([]byte(context)))

	// check if the file exists
	metaFile := filepath.Join(root, contextDir, "meta.json")
	if _, err := os.Stat(metaFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("context %s not found", context)
	}

	bytes, err := os.ReadFile(metaFile)
	if err != nil {
		return nil, err
	}

	var metadata ContextMetadata
	if err := json.Unmarshal(bytes, &metadata); err != nil {
		return nil, fmt.Errorf("could not parse context file for %s", context)
	}

	if metadata.Name != context {
		return nil, fmt.Errorf("found unexpected context %s for %s", metadata.Name, context)
	}

	return &metadata.Endpoints.Docker, nil
}

// https://github.com/docker/cli/blob/917d2dc837673ba6426ff72cabd53325028be809/cli/command/cli.go#L483
func GetDockerDaemon(context string) (*DockerConnection, error) {
	if context == defaultContextName {
		return resolveDefaultDockerDaemon()
	}
	return resolveDockerDaemon(context)
}

// https://github.com/docker/cli/blob/917d2dc837673ba6426ff72cabd53325028be809/cli/command/cli.go#L416
// Returns the current context in the following order:
//  1. The "DOCKER_CONTEXT" environment variable.
//  2. The current context as configured through the in "currentContext"
//     field in the CLI configuration file ("~/.docker/config.json").
//  3. If no context is configured, use the "default" context.
func GetCurrentContext() string {
	if context := os.Getenv("DOCKER_CONTEXT"); context != "" {
		return context
	}

	configFile := filepath.Join(conf.UserDockerDir(), "config.json")
	bytes, err := os.ReadFile(configFile)
	if err != nil {
		return "default"
	}

	var config struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(bytes, &config); err != nil {
		return "default"
	}
	if config.CurrentContext != "" {
		return config.CurrentContext
	}
	return "default"
}
