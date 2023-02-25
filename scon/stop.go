package main

import (
	"errors"
	"time"

	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

const (
	gracefulShutdownTimeoutRelease = 4 * time.Second
	gracefulShutdownTimeoutDebug   = 100 * time.Millisecond

	stopTimeout = 10 * time.Second
)

var (
	ErrTimeout = errors.New("start/stop timeout")
)

func (c *Container) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.Running() {
		return nil
	}

	logrus.WithField("container", c.Name).Info("stopping container")

	// must unfreeze so agent responds
	err := c.lxc.Unfreeze()
	if err != nil && err != lxc.ErrNotFrozen {
		return err
	}

	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}
	c.lastListeners = nil

	// ignore failure
	timeout := gracefulShutdownTimeoutRelease
	if conf.Debug() {
		timeout = gracefulShutdownTimeoutDebug
	}
	err = c.lxc.Shutdown(timeout)
	if err != nil {
		logrus.Warn("graceful shutdown failed: ", err)
	}

	if c.lxc.Running() {
		// release the lock. lxc.Stop() blocks until hook exits
		c.mu.Unlock()
		err = c.lxc.Stop()
		c.mu.Lock()
		if err != nil {
			return err
		}
	}

	if !c.lxc.Wait(lxc.STOPPED, stopTimeout) {
		return ErrTimeout
	}

	err = c.onStop()
	if err != nil {
		return err
	}

	// stop agent (after listeners removed and processes reaped)
	agent := c.agent.Get()
	if agent != nil {
		logrus.WithField("container", c.Name).Debug("stopping agent")
		agent.Close()
		c.agent.Set(nil)
	}

	logrus.WithField("container", c.Name).Info("stopped container")
	return nil
}

func (c *Container) onStop() error {
	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}
	c.lastListeners = nil

	// stop agent (after listeners removed)
	agent := c.agent.Get()
	if agent != nil {
		agent.Close()
		c.agent.Set(nil)
	}

	c.state = ContainerStateStopped

	// update & persist state IF manager isn't shutting down
	if !c.manager.stopping {
		err := c.persist()
		if err != nil {
			return err
		}
	}

	return nil
}
