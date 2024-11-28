package dockerclient

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orbstack/macvirt/vmgr/conf"
)

// holds raw bytes
type TLSData struct {
	CA   []byte
	Key  []byte
	Cert []byte
}

type EndpointMeta struct {
	Host          string `json:",omitempty"`
	SkipTLSVerify bool
}

type Endpoint struct {
	EndpointMeta
	TLSData *TLSData
}

type ContextMetadata struct {
	Name      string
	Endpoints struct {
		// the endpoint will always be "Docker":
		// https://github.com/docker/cli/blob/ba1a15433be5fe91edacd09b0c133f5f137ba5e2/cli/context/docker/constants.go#L5
		Docker EndpointMeta
	}
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

const defaultContextName = "default"
const defaultHost = "unix:///var/run/docker.sock"
const defaultTLSHost = "tcp://localhost:2376"

// since we do not support cli flags for TLSOptions, we only need
// to check DOCKER_CERT_PATH
func getTLSOptions() (tlsData *TLSData, err error) {
	path := os.Getenv("DOCKER_CERT_PATH")
	if path == "" {
		return nil, nil
	}

	return tlsDataFromFiles(
		filepath.Join(path, "ca.pem"),
		filepath.Join(path, "cert.pem"),
		filepath.Join(path, "key.pem"),
	)
}

func getHost(tlsData *TLSData) (host string, err error) {
	hostOverride := os.Getenv("DOCKER_HOST")
	if hostOverride != "" {
		return hostOverride, nil
	}

	if tlsData != nil {
		return defaultTLSHost, nil
	} else {
		return defaultHost, nil
	}
}

func getContextMetadata(context string) (ContextMetadata, error) {
	root := filepath.Join(conf.UserDockerDir(), "contexts", "meta")
	contextDir := fmt.Sprintf("%x", sha256.Sum256([]byte(context)))

	// check if the file exists
	metaFile := filepath.Join(root, contextDir, "meta.json")
	if _, err := os.Stat(metaFile); os.IsNotExist(err) {
		return ContextMetadata{}, fmt.Errorf("context %s not found", context)
	}

	bytes, err := os.ReadFile(metaFile)
	if err != nil {
		return ContextMetadata{}, fmt.Errorf("could not read context file for %s", context)
	}

	var metadata ContextMetadata
	if err := json.Unmarshal(bytes, &metadata); err != nil {
		return ContextMetadata{}, fmt.Errorf("could not parse context file for %s", context)
	}

	return metadata, nil
}

func getTLSData(context string) (*TLSData, error) {
	root := filepath.Join(conf.UserDockerDir(), "contexts", "tls")
	contextDir := fmt.Sprintf("%x", sha256.Sum256([]byte(context)))
	certPath := filepath.Join(root, contextDir, "docker")

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return nil, nil
	}

	return tlsDataFromFiles(
		filepath.Join(certPath, "ca.pem"),
		filepath.Join(certPath, "cert.pem"),
		filepath.Join(certPath, "key.pem"),
	)
}

// note: this implementation differs slightly from the official docker cli, because
// we do not need to support cli flags for Host and TLSOptions
// https://github.com/docker/cli/blob/ba1a15433be5fe91edacd09b0c133f5f137ba5e2/cli/command/cli.go#L347
func resolveDefaultDockerEndpoint() (Endpoint, error) {
	tlsData, err := getTLSOptions()
	if err != nil {
		return Endpoint{}, err
	}

	host, err := getHost(tlsData)
	if err != nil {
		return Endpoint{}, err
	}

	skipTLSVerify := os.Getenv("DOCKER_TLS_VERIFY") == ""
	return Endpoint{EndpointMeta: EndpointMeta{Host: host, SkipTLSVerify: skipTLSVerify}, TLSData: tlsData}, nil
}

// based off of the docker cli implementation:
// https://github.com/docker/cli/blob/ba1a15433be5fe91edacd09b0c133f5f137ba5e2/cli/command/cli.go#L331
func resolveDockerEndpoint(context string) (Endpoint, error) {
	ctxMeta, err := getContextMetadata(context)
	if err != nil {
		return Endpoint{}, err
	}

	tlsData, err := getTLSData(context)
	if err != nil {
		return Endpoint{}, err
	}

	return Endpoint{EndpointMeta: ctxMeta.Endpoints.Docker, TLSData: tlsData}, nil
}

// https://github.com/docker/cli/blob/917d2dc837673ba6426ff72cabd53325028be809/cli/command/cli.go#L483
func GetDockerEndpoint(context string) (Endpoint, error) {
	if context == defaultContextName {
		return resolveDefaultDockerEndpoint()
	}
	return resolveDockerEndpoint(context)
}

func getConfigCurrentContext() (string, error) {
	configFile := filepath.Join(conf.UserDockerDir(), "config.json")
	bytes, err := os.ReadFile(configFile)
	if err != nil {
		return "", err
	}

	var config struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(bytes, &config); err != nil {
		return "", err
	}

	return config.CurrentContext, nil
}

// https://github.com/docker/cli/blob/917d2dc837673ba6426ff72cabd53325028be809/cli/command/cli.go#L416
// CurrentContext returns the current context name, based on flags,
// environment variables and the cli configuration file, in the following
// order of preference:
//
//  1. The "--context" command-line option.
//  2. The "DOCKER_CONTEXT" environment variable ([EnvOverrideContext]).
//  3. The current context as configured through the in "currentContext"
//     field in the CLI configuration file ("~/.docker/config.json").
//  4. If no context is configured, use the "default" context.
//
// # Fallbacks for backward-compatibility
//
// To preserve backward-compatibility with the "pre-contexts" behavior,
// the "default" context is used if:
//
//   - The "--host" option is set
//   - The "DOCKER_HOST" ([client.EnvOverrideHost]) environment variable is set
//     to a non-empty value.
//
// In these cases, the default context is used, which uses the host as
// specified in "DOCKER_HOST", and TLS config from flags/env vars.
//
// Setting both the "--context" and "--host" flags is ambiguous and results
// in an error when the cli is started.
//
// CurrentContext does not validate if the given context exists or if it's
// valid; errors may occur when trying to use it.
func ResolveContextName(cliContext string) string {
	if cliContext != "" {
		return cliContext
	}
	if context := os.Getenv("DOCKER_HOST"); context != "" {
		return defaultContextName
	}
	if context := os.Getenv("DOCKER_CONTEXT"); context != "" {
		return context
	}
	configContext, err := getConfigCurrentContext()
	if err == nil {
		return configContext
	}

	return defaultContextName
}

// https://github.com/docker/cli/blob/ba1a15433be5fe91edacd09b0c133f5f137ba5e2/cli/context/tlsdata.go#L74
// tlsDataFromFiles reads files into a TLSData struct (or returns nil if all paths are empty)
func tlsDataFromFiles(caPath, certPath, keyPath string) (*TLSData, error) {
	var (
		ca, cert, key []byte
		err           error
	)
	if caPath != "" {
		ca, err = os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
	}
	if certPath != "" {
		cert, err = os.ReadFile(certPath)
		if err != nil {
			return nil, err
		}
	}
	if keyPath != "" {
		key, err = os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
	}
	if ca == nil && cert == nil && key == nil {
		return nil, nil
	}
	return &TLSData{CA: ca, Cert: cert, Key: key}, nil
}

// based off https://github.com/docker/cli/blob/ba1a15433be5fe91edacd09b0c133f5f137ba5e2/cli/context/docker/load.go#L43
func (ep *Endpoint) tlsConfig() (*tls.Config, error) {
	if ep.TLSData == nil && !ep.SkipTLSVerify {
		return nil, nil
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(ep.TLSData.CA) {
		return nil, fmt.Errorf("failed to append CA certificate")
	}

	cert, err := tls.X509KeyPair(ep.TLSData.Cert, ep.TLSData.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate/key: %w", err)
	}

	return &tls.Config{
		InsecureSkipVerify: ep.SkipTLSVerify,
		RootCAs:            caCertPool,
		Certificates:       []tls.Certificate{cert},
	}, nil
}
