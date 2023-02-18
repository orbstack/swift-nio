//go:build arm64

package arch

import (
	"strings"

	"github.com/Code-Hex/vz/v3"
	"github.com/sirupsen/logrus"
)

func CreateRosettaDevice() (*RosettaResult, error) {
	switch vz.LinuxRosettaDirectoryShareAvailability() {
	case vz.LinuxRosettaAvailabilityNotInstalled:
		err := vz.LinuxRosettaDirectoryShareInstallRosetta()
		if err != nil {
			logrus.WithError(err).Warn("failed to install Rosetta")
			if strings.HasPrefix(err.Error(), "Error Domain=VZErrorDomain Code=9 ") {
				return &RosettaResult{
					InstallCanceled: true,
				}, nil
			} else {
				return nil, nil
			}
		}
		fallthrough
	case vz.LinuxRosettaAvailabilityInstalled:
		rosettaDir, err := vz.NewLinuxRosettaDirectoryShare()
		if err != nil {
			return nil, err
		}

		virtiofsRosetta, err := vz.NewVirtioFileSystemDeviceConfiguration("rosetta")
		if err != nil {
			return nil, err
		}
		virtiofsRosetta.SetDirectoryShare(rosettaDir)
		return &RosettaResult{
			FsDevice: virtiofsRosetta,
		}, nil
	default:
		return nil, nil
	}
}
