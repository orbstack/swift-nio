package util

import (
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
	err = watcher.AddWatch(filepath.Dir(path), inotify.InCreate|inotify.InMovedTo)
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
			fmt.Println("event:", event)
			if event.Name == path {
				return nil
			}
		case err := <-watcher.Error:
			return fmt.Errorf("inotify error: %w", err)
		}
	}
}
