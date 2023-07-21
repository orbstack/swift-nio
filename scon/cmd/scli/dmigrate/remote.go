package dmigrate

import (
	"fmt"
	"net"
	"time"
)

const (
	remoteStartTimeout = 30 * time.Second
	remoteStartPoll    = 150 * time.Millisecond
)

func RemoteIsRunning(sock string) bool {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func WaitForRemote(sock string) error {
	start := time.Now()
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			conn.Close()
			return nil
		}

		if time.Since(start) > remoteStartTimeout {
			return fmt.Errorf("timeout waiting for remote: %w", err)
		}
		time.Sleep(remoteStartPoll)
	}
}
