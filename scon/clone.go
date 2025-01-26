package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
)

func (c *Container) Clone(newName string) (_ *Container, retErr error) {
	if c.builtin {
		return nil, errors.New("cannot clone builtin machine")
	}

	newC, _, err := c.manager.beginCreate(&types.CreateRequest{
		Name:   newName,
		Image:  c.Image,
		Config: c.config,
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			err2 := newC.deleteInternal()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container clone")
			}
		}
	}()

	if c.Freezer() != nil {
		// should never happen, as only builtin containers have freezers
		return nil, errors.New("cannot clone machine with freezer")
	}

	// add a mutation hold to prevent rootfs from changing
	// unlike c.mu.RLock(), this allows cancellation by deleting the source machine, and doesn't inhibit other actions like stopping
	var oldName string
	err = c.holds.WithHold("clone", func() error {
		// freeze old container to get a consistent data snapshot
		err := c.Freeze()
		if err != nil && !errors.Is(err, ErrMachineNotRunning) {
			return fmt.Errorf("freeze: %w", err)
		}
		defer c.Unfreeze()

		oldName = c.Name // consistent with rootfs copy

		// acquire jobs on both old and new containers, for cancellation
		err = c.jobManager.Run(func(ctx context.Context) error {
			return newC.jobManager.RunContext(ctx, func(ctx context.Context) error {
				err := util.RunContext(ctx, mounts.Starry, "cp", c.rootfsDir, newC.rootfsDir)
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
	err = agent.WriteHostnameFiles(newC.rootfsDir, oldName, newName, false /*runCommands*/)
	if err != nil {
		// soft fail for clone
		logrus.WithError(err).WithField("container", newC.Name).Error("failed to update hostname")
	}

	// add to NFS
	// restoring the container doesn't call this if state=creating
	err = c.manager.onRestoreContainer(newC)
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
