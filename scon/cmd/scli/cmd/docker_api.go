package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/orbstack/macvirt/vmgr/dockerclient"
)

func getDockerConfigDir() string {
	if dockerConfig := os.Getenv("DOCKER_CONFIG"); dockerConfig != "" {
		return dockerConfig
	}
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(home, ".docker")
}

// https://github.com/docker/cli/blob/30e9abbd3f78d2df1ffd0163fd6eb2a9e4fbbe11/cli/context/store/metadatastore.go#L21
func isContextDir(path string) bool {
	s, err := os.Stat(filepath.Join(path, "meta.json"))
	if err != nil {
		return false
	}
	return !s.IsDir()
}

type TLSData struct {
	CA   []byte
	Key  []byte
	Cert []byte
}

type ContextMetadata struct {
	Name      string
	Endpoints struct {
		Docker struct {
			Host          string
			SkipTLSVerify bool
			TLSData       *TLSData
		}
	}
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type DockerApiClient struct {
	client *http.Client
}

func createUnixSocketClient(addr string) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", addr)
		},
	}

	return &http.Client{
		Transport: transport,
		// Timeout:   30 * time.Second,
	}
}

func GetDockerClient(context string) (*dockerclient.Client, error) {
	root := filepath.Join(getDockerConfigDir(), "contexts", "meta")
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

			if metadata.Name != context {
				continue
			}

			return dockerclient.NewClient(metadata.Endpoints.Docker.Host)
		}
	}
	return nil, nil
}

func (c *DockerApiClient) Ping() error {
	res, err := c.client.Get("/_ping")

	if err != nil {
		return err
	}
	body, err := io.ReadAll(res.Body)
	fmt.Printf("ping: {%s}\n", string(body))
	return nil
}
