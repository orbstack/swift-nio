package main

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/sirupsen/logrus"
)

func (c *Container) renameInternalLocked(newName string) (retS string, retErr error) {
	m := c.manager
	if _, ok := m.containersByName[newName]; ok {
		return "", fmt.Errorf("machine '%q' already exists", newName)
	}

	delete(m.containersByID, c.ID)
	delete(m.containersByName, c.Name)
	oldName := c.Name
	c.Name = newName
	// past this point we need to recover from errors by reverting
	defer func() {
		if retErr != nil {
			c.Name = oldName
			_ = c.manager.insertContainerLocked(c)
			_ = c.persist()
		}
	}()
	err := c.manager.insertContainerLocked(c)
	if err != nil {
		return "", err
	}

	err = c.persist()
	if err != nil {
		return "", err
	}

	// update NFS bind mount: unmount old, mount new
	err = c.manager.nfsForAll.Unmount(oldName)
	if err != nil {
		return "", err
	}
	// past this point, recover by remounting old name
	defer func() {
		if retErr != nil {
			_ = c.manager.nfsForAll.Unmount(newName)
			err2 := c.manager.nfsForAll.MountBind(c.rootfsDir, oldName)
			if err2 != nil {
				logrus.WithError(err2).Error("failed to remount old name after error")
			}
		}
	}()
	err = c.manager.nfsForAll.MountBind(c.rootfsDir, newName)
	if err != nil {
		return "", err
	}

	// update UTS name
	err = c.setLxcConfig("lxc.uts.name", newName)
	if err != nil {
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
	c.manager.containersMu.Lock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if newName == c.Name {
		// don't bother to rename if name is same
		c.manager.containersMu.Unlock()
		return nil
	}
	oldName, err := c.renameInternalLocked(newName)
	c.manager.containersMu.Unlock()
	if err != nil {
		return err
	}

	if c.runningLocked() {
		// if running, finish in the agent
		// more secure than attaching netns and writing files from our side,
		// because it avoids symlink escape races
		err = c.useAgentLocked(func(a *agent.Client) error {
			return a.UpdateHostname(oldName, newName)
		})
	} else {
		// if not running, it's safe to update files from our side w/o chroot
		err = agent.WriteHostnameFiles(c.rootfsDir, oldName, newName, false /*runCommands*/)
	}
	if err != nil {
		// hmm, try to rename back
		c.manager.containersMu.Lock() // c.mu is already locked
		_, err2 := c.renameInternalLocked(oldName)
		c.manager.containersMu.Unlock()
		if err2 != nil {
			logrus.WithError(err2).Error("failed to rename back after agent error")
		}
		return err
	}

	return nil
}
