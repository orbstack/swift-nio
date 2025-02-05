package fsops

import (
	"errors"
	"os"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const cZFS_SUPER_MAGIC = 0x2fc12fc1

var ErrUnsupported = errors.New("unsupported FS operation")

type noopOps struct {
	fsMagic int64
}

func (b *noopOps) CreateSubvolumeIfNotExists(fsSubpath string) error {
	return os.MkdirAll(fsSubpath, 0755)
}

func (b *noopOps) SnapshotSubvolume(srcSubpath, dstSubpath string) error {
	return ErrUnsupported
}

func (b *noopOps) ListSubvolumes(fsSubpath string) ([]types.ExportedMachineSubvolume, error) {
	// unsupported FS has no subvolumes
	return nil, nil
}

func (b *noopOps) DeleteSubvolumeRecursive(fsSubpath string) error {
	logrus.Warn("unsupported FS operation: DeleteSubvolumesRecursive")
	return nil
}

func (b *noopOps) ResizeToMax(fsPath string) error {
	logrus.Warn("unsupported FS operation: ResizeToMax")
	return nil
}

func (b *noopOps) DumpDebugInfo(fsPath string) (string, error) {
	logrus.Warn("unsupported FS operation: DumpDebugInfo")
	return "", nil
}

func (b *noopOps) Name() string {
	switch b.fsMagic {
	case unix.EXT4_SUPER_MAGIC:
		return "ext4"
	case unix.XFS_SUPER_MAGIC:
		return "xfs"
	case unix.F2FS_SUPER_MAGIC:
		return "f2fs"
	case cZFS_SUPER_MAGIC:
		return "zfs"

	default:
		return "unknown"
	}
}

var _ FSOps = &noopOps{}
