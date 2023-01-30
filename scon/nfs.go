package main

import (
	"os"
	"path"

	"github.com/kdrag0n/macvirt/scon/conf"
	"golang.org/x/sys/unix"
)

func (m *ConManager) onRestoreContainer(c *Container) error {
	// nfs bind mount
	err := func() error {
		m.nfsMu.Lock()
		defer m.nfsMu.Unlock()

		nfsRoot := conf.C().NfsRoot
		path := path.Join(nfsRoot, c.Name)
		err := os.Mkdir(path, 0755)
		if err != nil {
			return err
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
