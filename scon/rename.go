package main

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/sirupsen/logrus"
)

func (c *Container) renameInternal(newName string) (string, error) {
	m := c.manager
	m.containersMu.Lock()
	defer c.manager.containersMu.Unlock()

	if _, ok := m.containersByName[newName]; ok {
		return "", fmt.Errorf("machine '%q' already exists", newName)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	delete(m.containersByID, c.ID)
	delete(m.containersByName, c.Name)
	oldName := c.Name
	c.Name = newName
	err := c.manager.insertContainerLocked(c)
	if err != nil {
		c.Name = oldName
		c.manager.insertContainerLocked(c)
		return "", err
	}

	err = c.persist()
	if err != nil {
		c.Name = oldName
		c.manager.insertContainerLocked(c)
		return "", err
	}

	return oldName, nil
}

func (c *Container) Rename(newName string) error {
	logrus.WithField("container", c.Name).WithField("to", newName).Info("renaming container")

	// validate new name
	err := validateContainerName(newName)
	if err != nil {
		return err
	}

	// take all locks and rename the actual container first
	oldName, err := c.renameInternal(newName)
	if err != nil {
		return err
	}

	// finish in the agent
	// more secure than attaching netns and writing files from our side,
	// because it avoids symlink escape races
	// outside lock in case agent is broken
	err = c.UseAgent(func(a *agent.Client) error {
		return a.UpdateHostname(oldName, newName)
	})
	if err != nil {
		// hmm, try to rename back
		if _, err2 := c.renameInternal(oldName); err2 != nil {
			logrus.WithError(err2).Error("failed to rename back after agent error")
		}
		return err
	}

	return nil
}
