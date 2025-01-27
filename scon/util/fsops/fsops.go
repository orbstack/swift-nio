package fsops

import (
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const BCACHEFS_STATFS_MAGIC = 0xca451a4e

type FSOps interface {
	CreateSubvolumeIfNotExists(fsSubpath string) error
	DeleteSubvolumeRecursive(fsSubpath string) error

	ResizeToMax(fsPath string) error
	DumpDebugInfo(fsPath string) (string, error)
}

func NewForFS(fsPath string) (FSOps, error) {
	var stf unix.Statfs_t
	err := unix.Statfs(fsPath, &stf)
	if err != nil {
		return nil, err
	}

	switch stf.Type {
	case unix.BTRFS_SUPER_MAGIC:
		return &btrfsOps{
			mountpoint: fsPath,
		}, nil
	case BCACHEFS_STATFS_MAGIC:
		return &bcachefsOps{}, nil

	// allow other filesystems for dogfooding (ext4, xfs, f2fs, etc.)
	default:
		logrus.Warnf("using unsupported filesystem type: %d", stf.Type)
		return &noopOps{}, nil
	}
}
