package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/lxc/go-lxc"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/sirupsen/logrus"
)

const (
	gracefulShutdownTimeout = 5 * time.Second
)

var (
	ErrTimeout = errors.New("timeout")
)

type StopOptions struct {
	Kill bool
}

func (c *Container) stopLocked(opts StopOptions) (oldState types.ContainerState, err error) {
	oldState = c.RealState()
	if oldState != types.ContainerStateRunning {
		return oldState, nil
	}

	logrus.WithField("container", c.Name).Info("stopping container")

	// begin transition
	oldState, err = c.transitionStateLocked(types.ContainerStateStopping)
	if err != nil {
		return oldState, err
	}
	defer func() {
		if err != nil {
			c.revertStateLocked(oldState)
		}
	}()

	err = c.hooks.OnStop(c)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("failed to run on-stop hook")
	}

	// don't allow any more freezing, and unfreeze if frozen
	rt, err := c.RuntimeState()
	if err != nil {
		return oldState, err
	}
	if rt.freezer != nil {
		rt.freezer.Close()
	}

	if opts.Kill {
		err = c.lxc.Stop()
		if err != nil {
			return oldState, err
		}
	} else {
		// graceful attempt first; ignore failure
		err = c.lxc.Shutdown(gracefulShutdownTimeout)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Warn("graceful shutdown failed")

			// this blocks until hook exits, but keeping lock is ok because we run hook asynchronously
			err = c.lxc.Stop()
			if err != nil {
				return oldState, err
			}
		}
	}

	if !c.lxc.Wait(lxc.STOPPED, startStopTimeout) {
		return oldState, fmt.Errorf("stop '%s': %w", c.Name, ErrTimeout)
	}

	err = c.onStopLocked()
	if err != nil {
		return oldState, err
	}

	return oldState, nil
}

func (c *Container) Stop(opts StopOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.stopLocked(opts)
	return err
}

func (c *Container) onStopLocked() error {
	// remove from mDNS registry
	c.manager.net.mdnsRegistry.RemoveMachine(c)

	// discard runtime state
	rt := c.runtimeState.Swap(nil)
	if rt != nil {
		// stop forwards
		for key := range rt.activeForwards {
			err := c.manager.removeForward(c, rt, key)
			if err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"container": c.Name,
					"key":       key,
				}).Error("failed to remove forward after stop")
			}
		}

		rt.Close()
	}

	err := c.manager.net.portMonitor.RemoveCallback(c.ID)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("failed to remove port monitor callback")
	}

	_, err = c.transitionStateLocked(types.ContainerStateStopped)
	if err != nil {
		return err
	}

	go func() {
		err := c.manager.net.RefreshFlowtable()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to refresh FT")
		}
	}()

	err = c.hooks.PostStop(c)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("failed to run post-stop hook")
	}

	logrus.WithField("container", c.Name).Info("container stopped")
	return nil
}
