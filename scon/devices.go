package main

import (
	"errors"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

func (m *ConManager) addDeviceNodeAll(src string, dst string) error {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	errs := make([]error, 0)
	for _, c := range m.containersByID {
		err := c.addDeviceNode(src, dst)
		if err != nil && !errors.Is(err, lxc.ErrNotRunning) {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}

	return nil
}

func (m *ConManager) removeDeviceNodeAll(src string, dst string) error {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	errs := make([]error, 0)
	for _, c := range m.containersByID {
		err := c.removeDeviceNode(src, dst)
		if err != nil && !errors.Is(err, lxc.ErrNotRunning) {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}

	return nil
}

func (m *ConManager) handleDeviceEvent(event fsnotify.Event) {
	logrus.WithField("event", event).Debug("device event")

	switch {
	case strings.HasPrefix(event.Name, "/dev/loop"):
		if event.Op&fsnotify.Create != 0 {
			logrus.WithField("path", event.Name).Debug("loop device created")
			m.addDeviceNodeAll(event.Name, event.Name)
		} else if event.Op&fsnotify.Remove != 0 {
			logrus.WithField("path", event.Name).Debug("loop device removed")
			m.removeDeviceNodeAll(event.Name, event.Name)
		}
	default:
		return
	}
}

func (m *ConManager) runDeviceMonitor() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.Add("/dev")
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Create != 0 {
				m.handleDeviceEvent(event)
			} else if event.Op&fsnotify.Remove != 0 {
				m.handleDeviceEvent(event)
			}
		case err := <-watcher.Errors:
			logrus.WithError(err).Error("device watcher error")
		}
	}
}
