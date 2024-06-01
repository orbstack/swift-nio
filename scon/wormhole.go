package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type WormholeManager struct {
	mu           sync.Mutex
	sessionCount int32
	mounts       []string
}

func NewWormholeManager() *WormholeManager {
	return &WormholeManager{mu: sync.Mutex{}, sessionCount: 0}
}

func (m *WormholeManager) OnSessionStart() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessionCount++

	logrus.WithFields(logrus.Fields{"oldCount": m.sessionCount - 1, "newCount": m.sessionCount}).Debug("new wormhole session")

	if m.sessionCount != 1 {
		return nil
	}

	logrus.Debug("mounting wormhole")

	if !slices.Contains(m.mounts, mounts.WormholeOverlay) {
		err := unix.Mount("wormhole", mounts.WormholeOverlay, "overlay", unix.MS_NOATIME, "lowerdir=/opt/wormhole-rootfs,upperdir=/data/wormhole/overlay/upper,workdir=/data/wormhole/overlay/work")
		if err != nil {
			return err
		}

		m.mounts = append([]string{mounts.WormholeOverlay}, m.mounts...)
	}

	var err error

	for _, path := range []string{"orb/data", "store", "var"} {
		source := mounts.WormholeOverlayNix + "/" + path
		target := mounts.WormholeUnifiedNix + "/" + path

		if slices.Contains(m.mounts, target) {
			continue
		}

		err = unix.Mount(source, target, "", unix.MS_BIND, "")
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{"source": source, "target": target}).Error("failed to bind mount")
			break
		}

		m.mounts = append([]string{target}, m.mounts...)
	}

	if err != nil {
		var toRemove []int

		for i, path := range m.mounts {
			uerr := unix.Unmount(path, 0)
			if uerr != nil {
				logrus.WithError(uerr).WithField("path", path).Error("failed to unmount")
				continue
			}

			toRemove = append(toRemove, i)
		}

		var newMounts []string

		for i, path := range m.mounts {
			if !slices.Contains(toRemove, i) {
				newMounts = append(newMounts, path)
			}
		}

		m.mounts = newMounts

		return err
	}

	return nil
}

func (m *WormholeManager) OnSessionEnd() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionCount--

	logrus.WithFields(logrus.Fields{"oldCount": m.sessionCount + 1, "newCount": m.sessionCount}).Debug("discarding wormhole session")

	if m.sessionCount != 0 {
		return nil
	}

	logrus.Debug("unmounting wormhole")

	var failedMounts []string
	var errs []error

	for _, path := range m.mounts {
		err := unix.Unmount(path, 0)
		if err != nil {
			// this would be so much cleaner if we had rust iterators
			failedMounts = append(failedMounts, path)
			errs = append(errs, fmt.Errorf("unmount %#v: %w", path, err))
		}
	}

	m.mounts = failedMounts

	return errors.Join(errs...)
}

func (m *WormholeManager) NukeData() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessionCount > 0 {
		return errors.New("Please exit all Debug Shell sessions before using this command.")
	}

	err := deleteRootfs("/data/wormhole/overlay/upper")
	if err != nil {
		return err
	}

	return os.Mkdir("/data/wormhole/overlay/upper", 0)
}

func isNixContainer(rootfsFile *os.File) (bool, error) {
	fs, err := securefs.NewFromDirfd(rootfsFile)
	if err != nil {
		return false, err
	}
	// CAN'T CLOSE FS! it doesn't own the fd

	_, err = fs.Stat("/nix/store")
	return err == nil, nil
}
