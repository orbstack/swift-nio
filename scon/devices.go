package main

import (
	"errors"
	"path"

	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/inotify"
)

func (m *ConManager) addDeviceNodeAll(src string, dst string) error {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	errs := make([]error, 0)
	for _, c := range m.containersByID {
		if !c.Running() {
			continue
		}

		go func(c *Container) {
			logrus.WithFields(logrus.Fields{
				"container": c.Name,
				"dst":       dst,
			}).Debug("adding device node")
			err := c.addDeviceNode(src, dst)
			if err != nil && !errors.Is(err, lxc.ErrNotRunning) {
				errs = append(errs, err)
			}
		}(c)
	}

	return errors.Join(errs...)
}

func (m *ConManager) removeDeviceNodeAll(src string, dst string) error {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	errs := make([]error, 0)
	for _, c := range m.containersByID {
		if !c.Running() {
			continue
		}

		go func(c *Container) {
			logrus.WithFields(logrus.Fields{
				"container": c.Name,
				"dst":       dst,
			}).Debug("removing device node")
			err := c.removeDeviceNode(dst)
			if err != nil && !errors.Is(err, lxc.ErrNotRunning) {
				errs = append(errs, err)
			}
		}(c)
	}

	return errors.Join(errs...)
}

func (m *ConManager) handleDeviceEvent(event *inotify.Event) {
	logrus.WithField("event", event).Debug("device event")

	nodeName := path.Base(event.Name)
	if MatchesExtraDevice(nodeName) {
		if event.Mask&inotify.InCreate != 0 {
			logrus.WithField("path", event.Name).Debug("creating extra device")
			err := m.addDeviceNodeAll(event.Name, event.Name)
			if err != nil {
				logrus.WithError(err).Error("failed to add extra device")
			}
		} else if event.Mask&inotify.InDelete != 0 {
			logrus.WithField("path", event.Name).Debug("removing extra device")
			err := m.removeDeviceNodeAll(event.Name, event.Name)
			if err != nil {
				logrus.WithError(err).Error("failed to remove extra device")
			}
		}
	}
}

func (m *ConManager) runDeviceMonitor() error {
	watcher, err := inotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.AddWatch("/dev", inotify.InCreate|inotify.InDelete)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Event:
			if event.Mask&inotify.InCreate != 0 {
				m.handleDeviceEvent(event)
			} else if event.Mask&inotify.InDelete != 0 {
				m.handleDeviceEvent(event)
			}
		case err := <-watcher.Error:
			logrus.WithError(err).Error("device watcher error")
		}
	}
}
