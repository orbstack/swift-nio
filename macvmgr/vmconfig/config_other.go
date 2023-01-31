//go:build !darwin

package vmconfig

func (c *VmConfig) validatePlatform() error {
	return nil
}
