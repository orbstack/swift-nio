package main

import "github.com/sirupsen/logrus"

func (c *Container) Restart() error {
	logrus.WithField("container", c.Name).Info("restarting container")

	// stop
	err := c.Stop(StopOptions{})
	if err != nil {
		return err
	}

	// start
	err = c.Start()
	if err != nil {
		return err
	}

	return nil
}
