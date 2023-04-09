package main

import (
	"errors"
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/sirupsen/logrus"
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

func mountOneNfs(dataSrc string, nfsSubDst string) error {
	nfsRootRO := conf.C().NfsRootRO
	nfsRootRW := conf.C().NfsRootRW
	backingPath := nfsRootRW + "/" + nfsSubDst
	mountPath := nfsRootRO + "/" + nfsSubDst

	logrus.WithFields(logrus.Fields{
		"src": dataSrc,
		"dst": mountPath,
	}).Trace("mounting nfs")
	err := os.Mkdir(backingPath, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	// is it already mounted?
	isMounted, err := isMountpoint(mountPath)
	if err != nil {
		return err
	}
	if isMounted {
		// unmount first
		err = unix.Unmount(mountPath, unix.MNT_DETACH)
		if err != nil {
			return err
		}
	}

	// bind mount
	err = unix.Mount(dataSrc, mountPath, "", unix.MS_BIND, "")
	if err != nil {
		return err
	}

	return nil
}

func unmountOneNfs(nfsSubDst string) error {
	nfsRootRO := conf.C().NfsRootRO
	nfsRootRW := conf.C().NfsRootRW
	backingPath := nfsRootRW + "/" + nfsSubDst
	mountPath := nfsRootRO + "/" + nfsSubDst

	logrus.WithField("dst", mountPath).Debug("unmounting nfs")
	// unmount
	err := unix.Unmount(mountPath, unix.MNT_DETACH)
	if err != nil {
		return err
	}

	// remove directory
	err = os.Remove(backingPath)
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) onRestoreContainer(c *Container) error {
	// nfs bind mount
	err := func() error {
		m.nfsMu.Lock()
		defer m.nfsMu.Unlock()

		// docker is special
		if c.Name == ContainerDocker {
			return nil
		}

		err := mountOneNfs(c.rootfsDir, c.Name)
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

		// docker is special
		if c.Name == ContainerDocker {
			return nil
		}

		err := unmountOneNfs(c.Name)
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
