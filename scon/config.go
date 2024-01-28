package main

import "github.com/orbstack/macvirt/scon/types"

func (c *Container) SetConfig(config types.MachineConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.config = config
	err := c.persist()
	if err != nil {
		return err
	}

	return nil
}
