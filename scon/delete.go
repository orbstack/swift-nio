package main

import (
	"errors"
	"os"

	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func deleteRootfs(rootfs string) error {
	logrus.WithField("rootfs", rootfs).Debug("deleting rootfs")

	// delete the entire directory
	err := os.RemoveAll(rootfs)
	if err != nil {
		if errors.Is(err, unix.EPERM) {
			// remove immutable and append-only flags
			err = util.Run("chattr", "-R", "-ai", rootfs)
			if err != nil {
				return err
			}

			// try again
			err = os.RemoveAll(rootfs)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	return nil
}

func (c *Container) Delete() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lxc.Running() {
		c.mu.Unlock()
		err := c.Stop()
		c.mu.Lock()
		if err != nil {
			return err
		}
	}

	if c.builtin {
		return errors.New("cannot delete builtin machine")
	}

	logrus.WithField("container", c.Name).Info("deleting container")

	// set deleting in case of failure
	c.deleting = true
	c.persist()

	// unmount from nfs
	err := c.manager.onPreDeleteContainer(c)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("container pre-delete hook failed")
	}

	// delete the entire directory
	err = deleteRootfs(c.dir)
	if err != nil {
		return err
	}

	// delete log if not creating
	// leave it for debugging if creating
	if !c.creating {
		err = os.Remove(c.logPath())
		if err != nil {
			return err
		}
	}

	return c.manager.removeContainer(c)
}
