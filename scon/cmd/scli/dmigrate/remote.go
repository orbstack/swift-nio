package dmigrate

import (
	"errors"
	"time"

	"github.com/orbstack/macvirt/vmgr/dockerclient"
)

const (
	remoteStartTimeout = 30 * time.Second
	remoteStartPoll    = 150 * time.Millisecond
)

func RemoteIsRunning(sock string) bool {
	// must do this b/c docker desktop creates socket immediately
	srcClient, err := dockerclient.NewWithUnixSocket(sock)
	if err != nil {
		return false
	}
	defer srcClient.Close()

	err = srcClient.CallDiscard("GET", "/_ping", nil)
	return err == nil
}

func WaitForRemote(sock string) error {
	start := time.Now()
	for {
		if RemoteIsRunning(sock) {
			return nil
		}

		if time.Since(start) > remoteStartTimeout {
			return errors.New("timeout waiting for remote")
		}
		time.Sleep(remoteStartPoll)
	}
}
