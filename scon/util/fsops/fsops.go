package fsops

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const BCACHEFS_STATFS_MAGIC = 0xca451a4e

type FSOps interface {
	CreateSubvolumeIfNotExists(fsSubpath string) error
	DeleteSubvolumesRecursive(fsSubpath string) error
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

	// allow a few experimental filesystems for dogfooding
	case unix.EXT4_SUPER_MAGIC:
		fallthrough
	case unix.XFS_SUPER_MAGIC:
		fallthrough
	case unix.F2FS_SUPER_MAGIC:
		logrus.Warnf("using unsupported filesystem type: %d", stf.Type)
		return &noopOps{}, nil

	default:
		return nil, fmt.Errorf("unsupported filesystem type: %d", stf.Type)
	}
}
