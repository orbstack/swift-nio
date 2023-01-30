package main

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
