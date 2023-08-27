//go:build linux

package util

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"k8s.io/utils/inotify"
)

const (
	runPollTimeout  = 15 * time.Second
	runPollInterval = 25 * time.Millisecond
)

// like WaitForPathExist but polls and waits for /run to be mounted first
// needed for nixos
func WaitForRunPathExist(path string) error {
	// wait for /run mount
	start := time.Now()
	for {
		if IsMountpointSimple("/run") {
			break
		}
		time.Sleep(runPollInterval)

		if time.Since(start) > runPollTimeout {
			return fmt.Errorf("timeout waiting for /run mount")
		}
	}

	return WaitForPathExist(path, false)
}

func WaitForPathExist(path string, requireWriteClose bool) error {
	// skip watcher if exists
	// must lstat because systemd units are non-existent symlinks
	if _, err := os.Lstat(path); err == nil {
		return nil
	}

	// create watcher
	watcher, err := inotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// watch the parent directory
	// includes both create and rename (to cover systemd invocation units)
	parent := filepath.Dir(path)
	flags := inotify.InCreate | inotify.InMovedTo
	if requireWriteClose {
		flags |= inotify.InCloseWrite
	}
	err = watcher.AddWatch(parent, flags)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		// precaution: stop at /
		if parent == "/" {
			return err
		}

		err = WaitForPathExist(parent, false)
		if err != nil {
			return err
		}

		// ok, now parent exists, then retry
		err = watcher.AddWatch(parent, flags)
	}
	if err != nil {
		return err
	}

	// stat again in case of race
	if _, err := os.Lstat(path); err == nil {
		return nil
	}

	// wait for the file to be created
	for {
		select {
		case event := <-watcher.Event:
			if event.Name == path {
				if !requireWriteClose || event.Mask&inotify.InCloseWrite != 0 {
					return nil
				}
			}
		case err := <-watcher.Error:
			return fmt.Errorf("inotify error: %w", err)
		}
	}
}

func WaitForSocketConnectible(path string) error {
	start := time.Now()
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			conn.Close()
			return nil
		}

		time.Sleep(runPollInterval)

		if time.Since(start) > runPollTimeout {
			return fmt.Errorf("timeout waiting for socket %s", path)
		}
	}
}
