package main

import "errors"

// locks removed to prevent issues with freezer's locks, so these are currently the same
func (c *Container) Freeze() error {
	// builtin machines' freezing is controlled by Freezer
	if c.builtin {
		return errors.New("builtin machines cannot be frozen")
	}

	return c.freezeInternal()
}

func (c *Container) freezeInternal() error {
	return c.lxc.Freeze()
}

func (c *Container) Unfreeze() error {
	// builtin machines' freezing is controlled by Freezer
	if c.builtin {
		return errors.New("builtin machines cannot be frozen")
	}

	return c.unfreezeInternal()
}

func (c *Container) unfreezeInternal() error {
	return c.lxc.Unfreeze()
}
