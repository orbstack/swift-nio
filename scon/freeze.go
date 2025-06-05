package main

func (c *Container) freezeInternal() error {
	return c.lxc.Freeze()
}

func (c *Container) unfreezeInternal() error {
	return c.lxc.Unfreeze()
}
