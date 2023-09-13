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
	Force    bool
	Internal bool
}

func (c *Container) stopLocked(opts StopOptions) (oldState types.ContainerState, err error) {
	oldState = c.State()
	if !c.runningLocked() {
		return oldState, nil
	}

	if !opts.Internal && c.manager.stopping {
		return oldState, ErrStopping
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

	// must unfreeze so agent responds
	freezer := c.Freezer()
	if freezer != nil {
		freezer.incRefCLocked()
		// unfreeze to prevent it from getting stuck in case of error (failed stop)
		// we close the freezer so it's a no-op if it's not stuck
		defer freezer.decRefCLocked()
	}

	// tell agent to prepare for stop
	agent := c.agent.Load()
	if agent != nil {
		err := agent.SyntheticWarnStop()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to send stop warning to agent")
		}
	}

	// graceful attempt first; ignore failure
	if !opts.Force {
		err = c.lxc.Shutdown(gracefulShutdownTimeout)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Warn("graceful shutdown failed")
		}
	}

	if c.lxc.Running() {
		// this blocks until hook exits, but keeping lock is ok because we run hook asynchronously
		err = c.lxc.Stop()
		if err != nil {
			return oldState, err
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

func (c *Container) stopForManagerShutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.stopLocked(StopOptions{
		Internal: true,
	})
	return err
}

func (c *Container) onStopLocked() error {
	// discard freezer
	// we don't need a ref for agent because it's already stopped
	freezer := c.freezer.Swap(nil)
	if freezer != nil {
		freezer.Close()
	}

	// stop forwards
	for _, listener := range c.lastListeners {
		err := c.manager.removeForwardCLocked(c, listener)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"container": c.Name,
				"listener":  listener,
			}).Error("failed to remove forward after stop")
		}
	}
	c.lastListeners = nil

	// remove from mDNS registry
	c.manager.net.mdnsRegistry.RemoveMachine(c)
	// discard cached IP addresses
	c.ipAddrs = nil

	// stop bpf
	if c.bpf != nil {
		err := c.bpf.Close()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to clean up bpf")
		}
		c.bpf = nil
	}

	// discard init pid
	if c.initPidFile != nil {
		c.initPidFile.Close()
	}
	c.initPid = 0
	c.initPidFile = nil

	// cancel listener update
	c.autofwdDebounce.Cancel()

	// stop agent (after listeners removed and processes reaped)
	agent := c.agent.Swap(nil)
	if agent != nil {
		logrus.WithField("container", c.Name).Debug("stopping agent")
		agent.Close()
	}

	_, err := c.transitionStateLocked(types.ContainerStateStopped)
	if err != nil {
		return err
	}

	if c.hooks != nil {
		err := c.hooks.PostStop(c)
		if err != nil {
			return err
		}
	}

	logrus.WithField("container", c.Name).Info("container stopped")
	return nil
}
