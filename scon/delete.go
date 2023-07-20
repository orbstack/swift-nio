package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func deleteRootfs(rootfs string) error {
	logrus.WithField("rootfs", rootfs).Debug("deleting rootfs")

	// list and delete btrfs subvolumes first
	// lxd can leave read-only subvols: 
	rawList, err := util.WithDefaultOom2(func() (string, error) {
		// -o excludes volumes after it
		return util.RunWithOutput("btrfs", "subvolume", "list", rootfs)
	})
	if err != nil {
		return fmt.Errorf("list subvolumes: %w", err)
	}

	// delete any that fall under this path
	lines := strings.Split(rawList, "\n")
	// iterate in reverse order so the order is naturally correct
	deleteArgs := []string{"btrfs", "subvolume", "delete"}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !strings.HasPrefix(line, "ID") {
			continue
		}

		fields := strings.Fields(line)
		subvolPath := conf.C().DataFsDir + "/" + fields[8]
		if strings.HasPrefix(subvolPath, rootfs+"/") {
			deleteArgs = append(deleteArgs, subvolPath)
		}
	}

	if len(deleteArgs) > 3 {
		logrus.WithField("subvols", deleteArgs[3:]).Debug("deleting subvolumes")
		err = util.WithDefaultOom1(func() error {
			return util.Run(deleteArgs...)
		})
		if err != nil {
			return fmt.Errorf("delete subvolumes: %w", err)
		}
	}

	// delete the entire directory
	err = os.RemoveAll(rootfs)
	if err != nil {
		if errors.Is(err, unix.EPERM) {
			// remove immutable and append-only flags
			err = util.WithDefaultOom1(func() error {
				return util.Run("chattr", "-R", "-ai", rootfs)
			})
			if err != nil {
				return err
			}

			// try again
			err = os.RemoveAll(rootfs)
			if err != nil {
				return fmt.Errorf("remove all x2: %w", err)
			}
		} else {
			return fmt.Errorf("remove all: %w", err)
		}
	}

	return nil
}

func (c *Container) deleteDockerLocked() error {
	if c.manager.stopping {
		return ErrStopping
	}

	_, err := c.stopLocked(false /* internalStop */)
	if err != nil {
		return err
	}

	oldState, err := c.transitionStateLocked(types.ContainerStateDeleting)
	if err != nil {
		return err
	}
	// always restore old state - we go back after deleting
	defer c.revertStateLocked(oldState)

	logrus.WithField("container", c.Name).Info("deleting container data")

	// delete the entire directory
	err = deleteRootfs(conf.C().DockerDataDir)
	if err != nil {
		return fmt.Errorf("delete rootfs: %w", err)
	}

	return nil
}

func (c *Container) deleteLocked(isInternal bool) error {
	// exception for builtin: docker can be deleted (data only)
	if c.ID == ContainerIDDocker {
		return c.deleteDockerLocked()
	}

	if c.builtin {
		return errors.New("cannot delete builtin machine")
	}

	if c.manager.stopping {
		return ErrStopping
	}

	_, err := c.stopLocked(false /* internalStop */)
	if err != nil {
		return err
	}

	oldState, err := c.transitionStateInternalLocked(types.ContainerStateDeleting, isInternal)
	if err != nil {
		return err
	}
	defer func() {
		// restore old state if we failed
		if err != nil {
			c.revertStateLocked(oldState)
		}
	}()

	logrus.WithField("container", c.Name).Info("deleting container")

	// unmount from nfs
	err = c.manager.onPreDeleteContainer(c)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("container pre-delete hook failed")
	}

	// delete the entire directory
	err = deleteRootfs(c.dir)
	if err != nil {
		return fmt.Errorf("delete rootfs: %w", err)
	}

	return c.manager.removeContainer(c)
}

func (c *Container) Delete() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.deleteLocked(false)
}

func (c *Container) deleteInternal() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.deleteLocked(true)
}
