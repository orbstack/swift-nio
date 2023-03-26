package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func WatchCriticalFiles(stopCh chan<- StopType) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.AddWithEvents(conf.DataImage(), unix.NOTE_DELETE)
	if err != nil {
		return err
	}

	// resolve executable symlink
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	execPath, err := filepath.EvalSymlinks(selfExe)
	if err != nil {
		return err
	}
	err = watcher.AddWithEvents(execPath, unix.NOTE_DELETE)
	if err != nil {
		return err
	}

	// resolve volume if we're on one
	var volumePath string
	if strings.HasPrefix(execPath, "/Volumes/") {
		volumeName := strings.Split(execPath, "/")[2]
		volumePath = "/Volumes/" + volumeName
		err = watcher.AddWithEvents(volumePath, unix.NOTE_DELETE)
		if err != nil {
			return err
		}
	}

	for {
		select {
		case event := <-watcher.Events:
			logrus.Debugf("Critical file event: %#v", event)
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				if event.Name == execPath {
					logrus.Info("Executable deleted, exiting")
					// we could hang forver if we access any uncached code pages
					os.Exit(1)
				} else if event.Name == volumePath {
					logrus.Info("DMG volume ejected, exiting")
					// we could hang forver if we access any uncached code pages
					os.Exit(1)
				} else {
					logrus.Info("Data image deleted, stopping VM")
					// force is ok - data doesn't matter anymore
					stopCh <- StopForce
				}
				return nil
			}
		case err := <-watcher.Errors:
			return err
		}
	}
}
