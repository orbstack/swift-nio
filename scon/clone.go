package main

import (
	"errors"
	"fmt"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

func (c *Container) Clone(newName string) (_ *Container, retErr error) {
	if c.builtin {
		return nil, errors.New("cannot clone builtin container")
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
		return nil, errors.New("cannot clone container with freezer")
	}

	// freeze old container to get a consistent data snapshot
	err = c.Freeze()
	if err != nil && !errors.Is(err, ErrMachineNotRunning) {
		return nil, fmt.Errorf("freeze: %w", err)
	}
	defer c.Unfreeze()

	// also lock old container (for read only) to prevent starting (if stopped), deletion, and name change
	// TODO: context-based cancellation instead? plus start lock
	c.mu.RLock()
	oldName := c.Name // consistent with rootfs copy
	err = util.Run("/opt/orb/starry-cp", c.rootfsDir, newC.rootfsDir)
	c.mu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("copy rootfs: %w", err)
	}

	// update hostname
	err = agent.WriteHostnameFiles(newC.rootfsDir, oldName, newName, false /*runCommands*/)
	if err != nil {
		return nil, fmt.Errorf("update hostname: %w", err)
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
