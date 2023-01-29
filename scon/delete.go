package main

import (
	"errors"
	"os"

	"github.com/sirupsen/logrus"
)

func (c *Container) Delete() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.lxc.Running() {
		c.mu.Unlock()
		err := c.Stop()
		c.mu.Lock()
		if err != nil {
			return err
		}
	}

	if c.builtin {
		return errors.New("cannot delete builtin container")
	}

	logrus.WithField("container", c.Name).Info("deleting container")

	// set deleting in case of failure
	c.deleting = true
	c.persist()

	// delete the entire directory
	err := os.RemoveAll(c.dir)
	if err != nil {
		return err
	}

	// delete log if not creating
	// leave it for debugging if creating
	if !c.creating {
		err = os.Remove(c.logPath())
		if err != nil {
			return err
		}
	}

	return c.manager.removeContainer(c)
}
