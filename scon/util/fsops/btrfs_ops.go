package fsops

import (
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/btrfs"
	"github.com/sirupsen/logrus"
)

type btrfsOps struct{}

func (b *btrfsOps) CreateSubvolumeIfNotExists(fsSubpath string) error {
	// doesn't work properly with our legacy qgroup setup
	return os.MkdirAll(fsSubpath, 0755)
}

func (b *btrfsOps) DeleteSubvolumesRecursive(fsSubpath string) error {
	rawList, err := util.WithDefaultOom2(func() (string, error) {
		// -o excludes volumes after it
		return util.RunWithOutput("btrfs", "subvolume", "list", fsSubpath)
	})
	if err != nil {
		return fmt.Errorf("list subvolumes: %w", err)
	}

	// delete any that fall under this path
	lines := strings.Split(rawList, "\n")
	// iterate in reverse order so the order is naturally correct
	deleteArgs := []string{"btrfs", "subvolume", "delete"}
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !strings.HasPrefix(line, "ID") {
			continue
		}

		fields := strings.Fields(line)
		subvolPath := fsSubpath + "/" + fields[8]
		if strings.HasPrefix(subvolPath, fsSubpath+"/") {
			deleteArgs = append(deleteArgs, subvolPath)
		}
	}

	if len(deleteArgs) > 3 {
		logrus.WithField("subvols", deleteArgs[3:]).Debug("deleting subvolumes")
		err = util.WithDefaultOom1(func() error {
			return util.Run(deleteArgs...)
		})
		if err != nil {
			return fmt.Errorf("delete subvolumes: %w", err)
		}
	}

	return nil
}

func (b *btrfsOps) ResizeToMax(fsPath string) error {
	return btrfs.FilesystemResize(fsPath, "max")
}

func (b *btrfsOps) DumpDebugInfo(fsPath string) (string, error) {
	usage, err2 := util.RunWithOutput("btrfs", "filesystem", "usage", fsPath)
	if err2 != nil {
		logrus.WithError(err2).Error("failed to get FS usage")
		usage = fmt.Sprintf("[failed to get FS usage: %v]", err2)
	}

	qgroup, err2 := util.RunWithOutput("btrfs", "qgroup", "show", "-r", "-e", fsPath)
	if err2 != nil {
		logrus.WithError(err2).Error("failed to get QG info")
		qgroup = fmt.Sprintf("[failed to get QG info: %v]", err2)
	} else {
		// strip first line so it's less obvious what this is
		qgroup = strings.Join(strings.Split(qgroup, "\n")[1:], "\n")
	}

	// now try to recover, but don't auto reboot
	// usage=0 means to deallocate only unused data/metadata blocks and release them back to general alloc pool, so it doesn't require any scratch space to complete
	balanceOut, err2 := util.RunWithOutput("btrfs", "balance", "start", "-dusage=0", "-musage=0", fsPath)
	if err2 != nil {
		logrus.WithError(err2).Error("failed to recover FS space")
		balanceOut = fmt.Sprintf("[failed to recover FS space: %v]", err2)
	}

	return fmt.Sprintf("Usage:\n%s\n\nQG:\n%s\n\nRecovery: %s", usage, qgroup, balanceOut), nil
}

var _ FSOps = &btrfsOps{}
