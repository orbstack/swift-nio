//go:build darwin

package vmconfig

import (
	"fmt"

	"github.com/Code-Hex/vz/v3"
)

func (c *VmConfig) validatePlatform() error {
	min := vz.VirtualMachineConfigurationMinimumAllowedMemorySize() / 1024 / 1024
	max := vz.VirtualMachineConfigurationMaximumAllowedMemorySize() / 1024 / 1024
	if c.MemoryMiB < min || c.MemoryMiB > max {
		return fmt.Errorf("memory_mib must be between %d and %d", min, max)
	}
	return nil
}
