package main

import "github.com/lxc/go-lxc"

func (c *Container) isFrozenLocked() bool {
	return c.lxc.State() == lxc.FROZEN
}

func (c *Container) IsFrozen() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.isFrozenLocked()
}

func (c *Container) Freeze() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lxc.Freeze()
}

func (c *Container) Unfreeze() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lxc.Unfreeze()
}
