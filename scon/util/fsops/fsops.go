package fsops

import (
	"github.com/orbstack/macvirt/scon/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const BCACHEFS_STATFS_MAGIC = 0xca451a4e

type FSOps interface {
	CreateSubvolumeIfNotExists(fsSubpath string) error
	SnapshotSubvolume(srcSubpath, dstSubpath string) error
	ListSubvolumes(fsSubpath string) ([]types.ExportedMachineSubvolume, error)
	DeleteSubvolumeRecursive(fsSubpath string) error

	ResizeToMax(fsPath string) error
	DumpDebugInfo(fsPath string) (string, error)

	Name() string
}

func NewForFS(fsPath string) (FSOps, error) {
	var stf unix.Statfs_t
	err := unix.Statfs(fsPath, &stf)
	if err != nil {
		return nil, err
	}

	switch stf.Type {
	case unix.BTRFS_SUPER_MAGIC:
		return NewBtrfsOps(fsPath)
	case BCACHEFS_STATFS_MAGIC:
		return &bcachefsOps{}, nil

	// allow other filesystems for dogfooding (ext4, xfs, f2fs, etc.)
	default:
		logrus.Warnf("using unsupported filesystem type: %d", stf.Type)
		return &noopOps{fsMagic: stf.Type}, nil
	}
}
