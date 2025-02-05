package fsops

import (
	"os"
	"path"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type bcachefsOps struct{}

func (b *bcachefsOps) CreateSubvolumeIfNotExists(fsSubpath string) error {
	// if subvolume already exists, return
	if _, err := os.Stat(fsSubpath); err == nil {
		logrus.WithField("subvolume", fsSubpath).Debug("subvolume already exists")
		return nil
	}

	// create parent dirs
	err := os.MkdirAll(path.Dir(fsSubpath), 0755)
	if err != nil {
		logrus.WithError(err).WithField("subvolume", fsSubpath).Debug("failed to create parent dirs")
		return err
	}

	// create subvolume
	return util.Run("bcachefs", "subvolume", "create", fsSubpath)
}

func (b *bcachefsOps) SnapshotSubvolume(srcSubpath, dstSubpath string) error {
	return util.Run("bcachefs", "subvolume", "snapshot", srcSubpath, dstSubpath)
}

func (b *bcachefsOps) ListSubvolumes(fsSubpath string) ([]types.ExportedMachineSubvolume, error) {
	// bcachefs subvolumes are just fancy directories, so there's no way to list them
	return nil, nil
}

func (b *bcachefsOps) DeleteSubvolumeRecursive(fsSubpath string) error {
	// impossible to recurse... there's no command to list subvolumes
	return util.Run("bcachefs", "subvolume", "delete", fsSubpath)
}

func (b *bcachefsOps) ResizeToMax(fsPath string) error {
	return util.Run("bcachefs", "device", "resize", "/dev/vdb1")
}

func (b *bcachefsOps) DumpDebugInfo(fsPath string) (string, error) {
	logrus.Warn("unsupported FS operation: DumpDebugInfo")
	return "", nil
}

func (b *bcachefsOps) Name() string {
	return "bcachefs"
}

var _ FSOps = &bcachefsOps{}
