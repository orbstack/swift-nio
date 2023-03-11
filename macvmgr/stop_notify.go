package main

import (
	"github.com/fsnotify/fsnotify"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func WatchDataImage(stopCh chan<- StopType) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.AddWithEvents(conf.DataImage(), unix.NOTE_DELETE)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Events:
			logrus.Debugf("Data image event: %#v", event)
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				logrus.Info("Data image deleted, stopping VM")
				// force is ok - data doesn't matter anymore
				stopCh <- StopForce
				return nil
			}
		case err := <-watcher.Errors:
			return err
		}
	}
}
