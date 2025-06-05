package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/fsops"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
)

func (oldC *Container) Clone(newName string) (_ *Container, retErr error) {
	if oldC.builtin {
		return nil, errors.New("cannot clone builtin machine")
	}

	newC, _, err := oldC.manager.beginCreate(&types.CreateRequest{
		Name:   newName,
		Image:  oldC.Image,
		Config: oldC.config,
	})
	if err != nil {
		return nil, err
	}
	defer newC.holds.EndMutation()
	defer func() {
		if retErr != nil {
			err2 := newC.deleteInternal()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container clone")
			}
		}
	}()

	// add a mutation hold to prevent rootfs from changing
	// unlike c.mu.RLock(), this allows cancellation by deleting the source machine, and doesn't inhibit other actions like stopping
	var oldName string
	err = oldC.holds.WithHold("clone", func() error {
		// grab a name that's consistent with our new rootfs copy, in case a rename occurs before updateHostnameLocked
		oldName = oldC.Name

		// fastpath: first attempt a snapshot
		err := newC.createDataDirs(createDataDirsOptions{
			snapshotFromPath: oldC.dataDir,
		})
		if err != nil {
			if errors.Is(err, fsops.ErrUnsupported) {
				// fallthrough to slowpath
			} else {
				return fmt.Errorf("create data snapshot: %w", err)
			}
		} else {
			// success: no copy needed
			return nil
		}

		// slowpath: freeze and copy

		// create new data dir
		err = newC.createDataDirs(createDataDirsOptions{
			// starry-cp creates dest dir
			includeRootfsDir: false,
		})
		if err != nil {
			return fmt.Errorf("create data dir: %w", err)
		}

		// freeze old container to get a consistent data snapshot
		err = oldC.Freeze()
		if err != nil && !errors.Is(err, ErrMachineNotRunning) {
			return fmt.Errorf("freeze: %w", err)
		}
		defer oldC.Unfreeze()

		// acquire jobs on both old and new containers, for cancellation
		err = oldC.jobManager.Run(func(ctx context.Context) error {
			return newC.jobManager.RunContext(ctx, func(ctx context.Context) error {
				err := util.RunContext(ctx, mounts.Starry, "cp", oldC.rootfsDir, newC.rootfsDir)
				// prefer "context cancelled" over "signal: killed"
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			})
		})
		if err != nil {
			return fmt.Errorf("copy rootfs: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// update hostname
	err = newC.updateHostnameLocked(oldName, newName)
	if err != nil {
		// soft fail for clone
		logrus.WithError(err).WithField("container", newC.Name).Error("failed to update hostname")
	}

	// add to NFS
	// restoring the container doesn't call this if state=creating
	err = oldC.manager.onRestoreContainer(newC)
	if err != nil {
		return nil, fmt.Errorf("call restore hook: %w", err)
	}

	newC.mu.Lock()
	defer newC.mu.Unlock()

	_, err = newC.transitionStateInternalLocked(types.ContainerStateStopped, true /*isInternal*/)
	if err != nil {
		return nil, fmt.Errorf("transition state: %w", err)
	}

	return newC, nil
}
