package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/sysx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func deleteRootfs(rootfs string) error {
	logrus.WithField("rootfs", rootfs).Debug("deleting rootfs")

	// swapoff on all swapfiles
	// we can't get full path in /proc/swaps from root ns - it's not translated
	// shows up as path relative to container mount ns instead
	// so just disable all swapfiles in case this container has one
	// otherwise we can't unlink the swapfile (EPERM)
	swaps, err := os.ReadFile("/proc/swaps")
	if err != nil {
		return fmt.Errorf("read swaps: %w", err)
	}
	for _, line := range strings.Split(string(swaps), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Type column
		if fields[1] != "file" {
			continue
		}

		// disable this swap
		// fails with ENOENT if it's not in this container's rootfs
		err = sysx.Swapoff(rootfs + "/rootfs/" + fields[0])
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("swapoff: %w", err)
		}
	}

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

func (c *Container) deleteDockerLocked(k8sOnly bool) error {
	_, err := c.stopLocked(StopOptions{
		Force: true, // don't care about data
	})
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
	if !k8sOnly {
		err = deleteRootfs(conf.C().DockerDataDir)
		if err != nil {
			return fmt.Errorf("delete docker data: %w", err)
		}
	}
	err = deleteRootfs(conf.C().K8sDataDir)
	if err != nil {
		return fmt.Errorf("delete k8s data: %w", err)
	}

	return nil
}

// internal means this is to clean up a failed creation
func (c *Container) deleteLocked(isInternal bool) error {
	if c.manager.stopping {
		return ErrStopping
	}

	// exception for builtin: docker can be deleted (data only)
	if c.ID == ContainerIDDocker {
		return c.deleteDockerLocked(false /*k8sOnly*/)
	}

	if c.builtin {
		return errors.New("cannot delete builtin machine")
	}

	_, err := c.stopLocked(StopOptions{
		Force: true, // don't care about data
	})
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

func (c *Container) DeleteK8s() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.manager.stopping {
		return ErrStopping
	}

	return c.deleteDockerLocked(true /*k8sOnly*/)
}
