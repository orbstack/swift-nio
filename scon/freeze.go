package main

import "github.com/lxc/go-lxc"

func (c *Container) IsFrozen() bool {
	return c.lxc.State() == lxc.FROZEN
}

func (c *Container) freezeLocked() error {
	if !c.Running() {
		return ErrMachineNotRunning
	}

	return c.lxc.Freeze()
}

// locks removed to prevent issues with freezer's locks, so these are currently the same
func (c *Container) Freeze() error {
	return c.freezeLocked()
}

func (c *Container) unfreezeLocked() error {
	return c.lxc.Unfreeze()
}

func (c *Container) Unfreeze() error {
	return c.unfreezeLocked()
}
