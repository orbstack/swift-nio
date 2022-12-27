//go:build arm64

package arch

import "github.com/Code-Hex/vz/v3"

func CreateRosettaDevice() (*vz.VirtioFileSystemDeviceConfiguration, error) {
	switch vz.LinuxRosettaDirectoryShareAvailability() {
	case vz.LinuxRosettaAvailabilityNotInstalled:
		err := vz.LinuxRosettaDirectoryShareInstallRosetta()
		if err != nil {
			return nil, err
		}
		fallthrough
	case vz.LinuxRosettaAvailabilityInstalled:
		rosettaDir, err := vz.NewLinuxRosettaDirectoryShare()
		if err != nil {
			return nil, err
		}

		virtiofsRosetta, err := vz.NewVirtioFileSystemDeviceConfiguration("rosetta")
		virtiofsRosetta.SetDirectoryShare(rosettaDir)
		return virtiofsRosetta, nil
	default:
		return nil, nil
	}
}
