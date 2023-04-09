package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

const (
	gracefulShutdownTimeout = 5 * time.Second
)

var (
	ErrTimeout = errors.New("timeout")
)

func (c *Container) stopLocked(internalStop bool) (oldState types.ContainerState, err error) {
	if !c.Running() {
		return c.state, nil
	}

	if !internalStop && c.manager.stopping {
		return c.state, ErrStopping
	}

	logrus.WithField("container", c.Name).Info("stopping container")

	// begin transition
	oldState, err = c.setStateLocked(types.ContainerStateStopping)
	if err != nil {
		return oldState, err
	}
	defer func() {
		if err != nil {
			c.revertStateLocked(oldState)
		}
	}()

	// must unfreeze so agent responds
	err = c.lxc.Unfreeze()
	if err != nil && err != lxc.ErrNotFrozen {
		return oldState, err
	}

	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}
	c.lastListeners = nil

	// ignore failure
	err = c.lxc.Shutdown(gracefulShutdownTimeout)
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Warn("graceful shutdown failed")
	}

	if c.lxc.Running() {
		// release the lock. lxc.Stop() blocks until hook exits
		c.mu.Unlock()
		err = c.lxc.Stop()
		c.mu.Lock()
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

	logrus.WithField("container", c.Name).Info("stopped container")
	return oldState, nil
}

func (c *Container) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.stopLocked(false /* internalStop */)
	return err
}

func (c *Container) stopForShutdown() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.stopLocked(true /* internalStop */)
	return err
}

func (c *Container) onStopLocked() error {
	// discard freezer
	// we don't need a ref for agent because it's already stopped
	freezer := c.freezer
	if freezer != nil {
		freezer.Close()
	}
	c.freezer = nil

	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}
	c.lastListeners = nil

	// stop netlink diag
	if c.inetDiagFile != nil {
		c.inetDiagFile.Close()
		c.inetDiagFile = nil
	}

	// stop agent (after listeners removed and processes reaped)
	agent := c.agent.Get()
	if agent != nil {
		logrus.WithField("container", c.Name).Debug("stopping agent")
		agent.Close()
		c.agent.Set(nil)
	}

	_, err := c.setStateLocked(types.ContainerStateStopped)
	if err != nil {
		return err
	}

	return nil
}
