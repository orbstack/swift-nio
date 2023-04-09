package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/utils/inotify"
)

func WaitForPathExist(path string) error {
	// skip watcher if exists
	if _, err := os.Stat(path); err == nil {
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
	if _, err := os.Stat(path); err == nil {
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
