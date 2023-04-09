package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"k8s.io/utils/inotify"
)

const (
	runPollTimeout = 15 * time.Second
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
		time.Sleep(25 * time.Millisecond)

		if time.Since(start) > runPollTimeout {
			return fmt.Errorf("timeout waiting for /run mount")
		}
	}

	return WaitForPathExist(path)
}

func WaitForPathExist(path string) error {
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
	err = watcher.AddWatch(parent, inotify.InCreate|inotify.InMovedTo)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		// precaution: stop at /
		if parent == "/" {
			return err
		}

		err = WaitForPathExist(parent)
		if err != nil {
			return err
		}

		// ok, now parent exists, then retry
		err = watcher.AddWatch(parent, inotify.InCreate|inotify.InMovedTo)
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
				return nil
			}
		case err := <-watcher.Error:
			return fmt.Errorf("inotify error: %w", err)
		}
	}
}
