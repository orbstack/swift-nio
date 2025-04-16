package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/sysx"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// WARNING: os.RemoveAll is *NOT* safe against symlink races.
// It uses fstatat to check and only recurse into dirs, and unlink is symlink-safe.
// But uses openat without O_NOFOLLOW to open dirs, after the fstat.
//
// This doesn't matter for our use cases:
//   - container must be stopped
//   - c.mu is held for the duration of the call, so it can't be started
//
// ... but DO NOT USE this if the container could be running.
func (m *ConManager) deleteRootfs(rootfs string) error {
	logrus.WithField("rootfs", rootfs).Debug("deleting rootfs")

	// skip if already deleted / doesn't exist
	// can happen if creation was canceled
	if err := unix.Access(rootfs, unix.F_OK); err == unix.ENOENT {
		return nil
	}

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

	// list and delete btrfs subvolumes
	// lxd can leave read-only subvols:
	err = m.fsOps.DeleteSubvolumeRecursive(rootfs)
	if err != nil {
		return fmt.Errorf("delete subvolumes: %w", err)
	}

	// was the entire rootfs a subvolume? if so, it might've been deleted
	if err := unix.Access(rootfs, unix.F_OK); err == unix.ENOENT {
		return nil
	}

	// delete the entire directory
	// this takes care of immutable and append-only flags
	err = util.Run(mounts.Starry, "rm", rootfs)
	if err != nil {
		return fmt.Errorf("delete directory: %w", err)
	}

	return nil
}

func (c *Container) deleteDockerLocked(k8sOnly bool) error {
	_, err := c.stopLocked(StopOptions{
		KillProcesses: true, // don't care about data
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
		err = c.manager.deleteRootfs(conf.C().DockerDataDir)
		if err != nil {
			return fmt.Errorf("delete docker data: %w", err)
		}
	}
	err = c.manager.deleteRootfs(conf.C().K8sDataDir)
	if err != nil {
		return fmt.Errorf("delete k8s data: %w", err)
	}

	return nil
}

// internal means this is to clean up a failed creation
func (c *Container) deleteLocked(isInternal bool) error {
	if c.manager.stopping.Load() {
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
		KillProcesses: true, // don't care about data
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

	// kill and wait for long-running jobs associated with this container
	c.jobManager.Close()

	// unmount from nfs
	err = c.manager.onPreDeleteContainer(c)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("container pre-delete hook failed")
	}

	// delete the entire directory
	err = c.manager.deleteRootfs(c.dataDir)
	if err != nil {
		return fmt.Errorf("delete rootfs: %w", err)
	}

	// sync to make sure it's deleted before deleting from db
	parentFd, err := unix.Open(path.Dir(c.dataDir), unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			// should never happen unless FS gets very corrupted, but we can continue deleting
			logrus.Error("rootfs parent dir doesn't exist, deleting machine anyway")
		} else {
			return fmt.Errorf("open dir: %w", err)
		}
	}
	defer unix.Close(parentFd)

	err = unix.Fsync(int(parentFd))
	if err != nil {
		return fmt.Errorf("fsync dir: %w", err)
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

	if c.manager.stopping.Load() {
		return ErrStopping
	}

	return c.deleteDockerLocked(true /*k8sOnly*/)
}
