package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func mountOneNfs(source string, nfsSubDst string, fstype string, flags uintptr, data string) error {
	nfsRootRO := conf.C().NfsRootRO
	nfsRootRW := conf.C().NfsRootRW
	backingPath := nfsRootRW + "/" + nfsSubDst
	destPath := nfsRootRO + "/" + nfsSubDst

	logrus.WithFields(logrus.Fields{
		"src": source,
		"dst": destPath,
	}).Trace("mounting nfs")
	err := os.MkdirAll(backingPath, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	// unmount first
	err = unix.Unmount(destPath, unix.MNT_DETACH)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}

	// bind mount
	err = unix.Mount(source, destPath, fstype, flags, data)
	if err != nil {
		return err
	}

	return nil
}

func mountOneNfsBind(source string, nfsSubDst string) error {
	return mountOneNfs(source, nfsSubDst, "", unix.MS_BIND, "")
}

func unmountOneNfs(nfsSubDst string) error {
	nfsRootRO := conf.C().NfsRootRO
	nfsRootRW := conf.C().NfsRootRW
	backingPath := nfsRootRW + "/" + nfsSubDst
	mountPath := nfsRootRO + "/" + nfsSubDst

	logrus.WithField("dst", mountPath).Debug("unmounting nfs")
	// unmount
	err := unix.Unmount(mountPath, unix.MNT_DETACH)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		// EINVAL = not mounted
		return err
	}

	// remove directory
	err = os.Remove(backingPath)
	if err != nil {
		return err
	}

	return nil
}

func addNfsdExport(path string) error {
	// matches what exportfs -arv does
	err := os.WriteFile("/proc/net/rpc/auth.unix.ip/channel", []byte("nfsd 0.0.0.0 2147483647 -test-client-"), 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile("/proc/net/rpc/nfsd.export/channel", []byte(fmt.Sprintf("-test-client- %s  3 25662 65534 65534 0", path)), 0644)
	if err != nil {
		return err
	}

	// flush
	flushData := []byte(fmt.Sprintf("%d", time.Now().Unix()))
	err = os.WriteFile("/proc/net/rpc/auth.unix.ip/flush", flushData, 0644)
	if err != nil {
		return err
	}
	err = os.WriteFile("/proc/net/rpc/auth.unix.gid/flush", flushData, 0644)
	if err != nil {
		return err
	}
	err = os.WriteFile("/proc/net/rpc/nfsd.fh/flush", flushData, 0644)
	if err != nil {
		return err
	}
	err = os.WriteFile("/proc/net/rpc/nfsd.export/flush", flushData, 0644)
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
		if c.ID == ContainerIDDocker {
			return nil
		}

		err := mountOneNfsBind(c.rootfsDir, c.Name)
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
		if c.ID == ContainerIDDocker {
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

func bindMountNfsRoot(c *Container, src string, target string) error {
	return c.UseMountNs(func() error {
		return unix.Mount(src, target, "", unix.MS_BIND|unix.MS_REC|unix.MS_SHARED|unix.MS_RDONLY, "")
	})
}
