package fsops

import (
	"errors"
	"os"

	"github.com/sirupsen/logrus"
)

var ErrUnsupported = errors.New("unsupported FS operation")

type noopOps struct{}

func (b *noopOps) CreateSubvolumeIfNotExists(fsSubpath string) error {
	return os.MkdirAll(fsSubpath, 0755)
}

func (b *noopOps) SnapshotSubvolume(srcSubpath, dstSubpath string) error {
	return ErrUnsupported
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

var _ FSOps = &noopOps{}
