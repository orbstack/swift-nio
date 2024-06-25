package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var wormholeBindMounts = []string{"orb/data", "store", "var"}

type WormholeManager struct {
	mu           sync.Mutex
	sessionCount int32

	// either base + binds are mounted, or none are
	isMounted bool
}

func NewWormholeManager() *WormholeManager {
	return &WormholeManager{sessionCount: 0}
}

func (m *WormholeManager) OnSessionStart() (retErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessionCount++
	logrus.WithFields(logrus.Fields{"newCount": m.sessionCount}).Debug("new wormhole session")

	if m.sessionCount == 1 && !m.isMounted {
		logrus.Debug("mounting wormhole")

		err := unix.Mount("wormhole", mounts.WormholeOverlay, "overlay", unix.MS_NOATIME, "lowerdir=/opt/wormhole-rootfs,upperdir=/data/wormhole/overlay/upper,workdir=/data/wormhole/overlay/work")
		if err != nil {
			return err
		}
		defer func() {
			if retErr != nil {
				_ = unix.Unmount(mounts.WormholeOverlay, 0)
			}
		}()

		for _, subpath := range wormholeBindMounts {
			src := mounts.WormholeOverlayNix + "/" + subpath
			dst := mounts.WormholeUnifiedNix + "/" + subpath
			err := unix.Mount(src, dst, "", unix.MS_BIND, "")
			if err != nil {
				return fmt.Errorf("mount %s -> %s: %w", src, dst, err)
			}
			defer func() {
				if retErr != nil {
					_ = unix.Unmount(dst, 0)
				}
			}()
		}

		m.isMounted = true
	}

	return nil
}

func (m *WormholeManager) OnSessionEnd() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessionCount--
	logrus.WithFields(logrus.Fields{"newCount": m.sessionCount}).Debug("discarding wormhole session")

	if m.sessionCount == 0 && m.isMounted {
		logrus.Debug("unmounting wormhole")

		// detach all bind mounts first
		// should always succeed
		for _, path := range wormholeBindMounts {
			dst := mounts.WormholeUnifiedNix + "/" + path
			err := unix.Unmount(dst, unix.MNT_DETACH)
			if err != nil && !errors.Is(err, unix.EINVAL) {
				// EINVAL = not mounted (???)
				return fmt.Errorf("unmount %s: %w", dst, err)
			}
		}
		defer func() {
			if m.isMounted {
				// if we didn't unmount, restore bind mounts to make state consistent again
				for _, path := range wormholeBindMounts {
					src := mounts.WormholeOverlayNix + "/" + path
					dst := mounts.WormholeUnifiedNix + "/" + path
					err := unix.Mount(src, dst, "", unix.MS_BIND, "")
					if err != nil {
						logrus.WithError(err).Errorf("failed to restore bind mount %s -> %s", src, dst)
					}
				}
			}
		}()

		// try to unmount overlay
		err := unix.Unmount(mounts.WormholeOverlay, 0)
		if err != nil {
			// TODO: this never returns EBUSY, even if it's in use???
			if errors.Is(err, unix.EBUSY) {
				// leave it mounted and return success; don't change m.isMounted
				// this happens when background processes still have fds open
				logrus.Warn("wormhole still in use, skipping unmount")
				return nil
			} else if errors.Is(err, unix.EINVAL) {
				// not mounted (???)
				logrus.Warn("wormhole not mounted, skipping unmount")
			} else {
				// got a different error
				// this must mean the FS is broken, so don't try to recover bind mounts
				return err
			}
		}

		m.isMounted = false
	}

	return nil
}

func (m *WormholeManager) NukeData() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isMounted {
		// make staticcheck happy
		return fmt.Errorf("%s", "Please exit all Debug Shell sessions before using this command.")
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
