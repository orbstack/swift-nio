package wormclient

import (
	"encoding/json"
	"net/url"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
)

// returns the docker daemon, isLocal, error
func GetDaemon(context string) (*dockerclient.DockerConnection, bool, error) {
	if context == "" {
		context = dockerclient.GetCurrentContext()
	}
	daemon, err := dockerclient.GetDockerDaemon(context)
	if err != nil {
		return nil, false, err
	}

	// check if the daemon is local by comparing with the orbstack socket
	orbSocket := conf.DockerSocket()
	url, err := url.Parse(daemon.Host)
	if err != nil {
		return nil, false, err
	}

	isLocal := url.Scheme == "unix" && url.Host == orbSocket
	return daemon, isLocal, nil
}

func GetDrmToken() (string, error) {
	keychainData, err := drmcore.ReadKeychainDrmState()
	if err != nil {
		return "", err
	}

	var keychainState drmtypes.PersistentState
	err = json.Unmarshal(keychainData, &keychainState)
	if err != nil {
		return "", err
	}

	return keychainState.EntitlementToken, nil
}
