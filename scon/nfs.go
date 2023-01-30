package main

import (
	"errors"
	"os"
	"path"
	"strings"

	"github.com/kdrag0n/macvirt/scon/conf"
	"golang.org/x/sys/unix"
)

func isMountpoint(path string) (bool, error) {
	mountinfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(mountinfo), "\n") {
		parts := strings.Split(line, " ")
		if len(parts) < 5 {
			continue
		}

		if parts[4] == path {
			return true, nil
		}
	}

	return false, nil
}

func (m *ConManager) onRestoreContainer(c *Container) error {
	// nfs bind mount
	err := func() error {
		m.nfsMu.Lock()
		defer m.nfsMu.Unlock()

		nfsRoot := conf.C().NfsRoot
		path := path.Join(nfsRoot, c.Name)
		err := os.Mkdir(path, 0755)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}

		// is it already mounted?
		isMounted, err := isMountpoint(path)
		if err != nil {
			return err
		}
		if isMounted {
			// unmount first
			err = unix.Unmount(path, unix.MNT_DETACH)
			if err != nil {
				return err
			}
		}

		// bind mount
		err = unix.Mount(c.rootfsDir, path, "", unix.MS_BIND, "")
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) onPreDeleteContainer(c *Container) error {
	// nfs symlink
	err := func() error {
		m.nfsMu.Lock()
		defer m.nfsMu.Unlock()

		nfsRoot := conf.C().NfsRoot
		path := path.Join(nfsRoot, c.Name)

		// unmount
		err := unix.Unmount(path, unix.MNT_DETACH)
		if err != nil {
			return err
		}

		// remove directory
		err = os.Remove(path)
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		return err
	}

	return nil
}
