package arch

import "github.com/Code-Hex/vz/v3"

type RosettaResult struct {
	FsDevice        *vz.VirtioFileSystemDeviceConfiguration
	InstallCanceled bool
}
