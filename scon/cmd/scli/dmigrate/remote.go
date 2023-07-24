package dmigrate

import (
	"errors"
	"fmt"
	"time"

	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"golang.org/x/sys/unix"
)

const (
	remoteStartTimeout = 45 * time.Second
	remoteStartPoll    = 250 * time.Millisecond
)

func tryConnectRemote(sock string) error {
	// must use ping test b/c docker desktop creates socket immediately
	srcClient, err := dockerclient.NewWithUnixSocket(sock, &dockerclient.Options{
		Unversioned: true,
	})
	if err != nil {
		return err
	}
	defer srcClient.Close()

	return srcClient.CallDiscard("GET", "/_ping", nil)
}

func RemoteIsRunning(sock string) bool {
	return tryConnectRemote(sock) == nil
}

func WaitForRemote(sock string) error {
	start := time.Now()
	for {
		err := tryConnectRemote(sock)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.ECONNREFUSED) && !errors.Is(err, unix.ENOENT) {
			return err
		}

		if time.Since(start) > remoteStartTimeout {
			return fmt.Errorf("timeout waiting for remote: %w", err)
		}
		time.Sleep(remoteStartPoll)
	}
}
