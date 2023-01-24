package main

import (
	"errors"
	"os"
	"time"

	_ "net/http/pprof"

	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
)

const (
	gracefulShutdownTimeoutRelease = 3 * time.Second
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

	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}
	c.lastListeners = nil

	// stop agent (after listeners removed)
	if c.agent.Get() != nil {
		c.Agent().Close()
		c.agent.Set(nil)
	}

	// ignore failure
	timeout := gracefulShutdownTimeoutRelease
	if conf.Debug() {
		timeout = gracefulShutdownTimeoutDebug
	}
	err := c.c.Shutdown(timeout)
	if err != nil {
		logrus.Warn("graceful shutdown failed: ", err)
	}

	err = c.c.Stop()
	if err != nil {
		return err
	}

	if !c.c.Wait(lxc.STOPPED, stopTimeout) {
		return ErrTimeout
	}

	err = c.onStop()
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) onStop() error {
	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}
	c.lastListeners = nil

	// stop agent (after listeners removed)
	if c.agent.Get() != nil {
		c.Agent().Close()
		c.agent.Set(nil)
	}

	// update & persist state
	err := c.persist()
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) Delete() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.c.Running() {
		c.mu.Unlock()
		err := c.Stop()
		c.mu.Lock()
		if err != nil {
			return err
		}
	}

	logrus.WithField("container", c.Name).Info("deleting container")

	// set deleting in case of failure
	c.deleting = true
	c.persist()

	// delete lxc
	err := c.c.Destroy()
	if err != nil {
		return err
	}

	// delete the entire directory
	err = os.RemoveAll(c.dir)
	if err != nil {
		return err
	}

	return c.manager.removeContainer(c)
}
