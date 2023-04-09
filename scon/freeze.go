package main

import "github.com/lxc/go-lxc"

func (c *Container) IsFrozen() bool {
	return c.lxc.State() == lxc.FROZEN
}

// locks removed to prevent issues with freezer's locks
func (c *Container) Freeze() error {
	if !c.Running() {
		return ErrNotRunning
	}

	return c.lxc.Freeze()
}

func (c *Container) Unfreeze() error {
	return c.lxc.Unfreeze()
}
