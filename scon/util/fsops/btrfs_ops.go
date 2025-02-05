package fsops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/btrfs"
	"github.com/sirupsen/logrus"
)

// in btrfs qgroups, higher numbers are higher-level
// 0/5 is the root subvolume
// 0/... is each subvolume
// 1/1 is our global qgroup that contains all subvolumes
const qgroupGlobal = "1/1"

type btrfsOps struct {
	mountpoint    string
	useSubvolumes bool
}

func NewBtrfsOps(mountpoint string) (FSOps, error) {
	// use subvolumes if we're on the new qgroup setup (i.e. if 1/1 exists)
	listOutput, err := util.RunWithOutput("btrfs", "qgroup", "show", mountpoint)
	if err != nil {
		return nil, fmt.Errorf("list qg: %w", err)
	}

	useSubvolumes := false
	for _, line := range strings.Split(listOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		if fields[0] == "1/1" {
			useSubvolumes = true
			break
		}
	}

	logrus.WithField("useSubvolumes", useSubvolumes).Debug("detected btrfs")
	return &btrfsOps{mountpoint: mountpoint, useSubvolumes: useSubvolumes}, nil
}

func (b *btrfsOps) CreateSubvolumeIfNotExists(fsSubpath string) error {
	// if subvolume already exists, return
	if _, err := os.Stat(fsSubpath); err == nil {
		logrus.WithField("subvolume", fsSubpath).Debug("subvolume already exists")
		return nil
	}

	if b.useSubvolumes {
		// -p = equivalent to mkdir -p
		return util.Run("btrfs", "subvolume", "create", "-p", "-i", qgroupGlobal, fsSubpath)
	} else {
		// doesn't work properly with our legacy qgroup setup
		return os.MkdirAll(fsSubpath, 0o755)
	}
}

func btrfsPathIsSubvolume(path string) (bool, error) {
	// it's a subvolume if st_dev differs from parent
	stat, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	parentStat, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	return stat.Sys().(*syscall.Stat_t).Dev != parentStat.Sys().(*syscall.Stat_t).Dev, nil
}

func (b *btrfsOps) SnapshotSubvolume(srcSubpath, dstSubpath string) error {
	isSrcSubvol, err := btrfsPathIsSubvolume(srcSubpath)
	if err != nil {
		return fmt.Errorf("check if src is subvolume: %w", err)
	}
	if !isSrcSubvol {
		// original path was not a subvolume, so fall back to cp
		logrus.WithField("src", srcSubpath).WithField("dst", dstSubpath).Debug("skipping snapshot: not a subvolume")
		return ErrUnsupported
	}

	return util.Run("btrfs", "subvolume", "snapshot", srcSubpath, dstSubpath)
}

func (b *btrfsOps) ListSubvolumes(fsSubpath string) ([]types.ExportedMachineSubvolume, error) {
	rawList, err := util.WithDefaultOom2(func() (string, error) {
		// -o excludes volumes after it
		return util.RunWithOutput("btrfs", "subvolume", "list", fsSubpath)
	})
	if err != nil {
		return nil, err
	}

	lines := strings.Split(rawList, "\n")
	subvols := make([]types.ExportedMachineSubvolume, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !strings.HasPrefix(line, "ID") {
			continue
		}

		fields := strings.Fields(line)
		subvolPath := b.mountpoint + "/" + fields[8]
		if strings.HasPrefix(subvolPath, fsSubpath+"/") {
			subvols = append(subvols, types.ExportedMachineSubvolume{
				Path: subvolPath,
			})
		}
	}

	return subvols, nil
}

func (b *btrfsOps) DeleteSubvolumeRecursive(fsSubpath string) error {
	// if src is a subvolume, then just use btrfs recursive delete
	isSrcSubvol, err := btrfsPathIsSubvolume(fsSubpath)
	if err != nil {
		return fmt.Errorf("check if src is subvolume: %w", err)
	}
	if isSrcSubvol {
		// -c = commit after deleting all subvolumes
		// container deletion requires this for consistency with db
		return util.Run("btrfs", "subvolume", "delete", "-c", "-R", fsSubpath)
	}

	// fallback: src is not a subvolume, but user may have created subvolumes under it, so we need to check and delete those
	subvols, err := b.ListSubvolumes(fsSubpath)
	if err != nil {
		return fmt.Errorf("list subvolumes: %w", err)
	}

	if len(subvols) > 0 {
		// reverse order for correct deletion
		deleteArgs := []string{"btrfs", "subvolume", "delete", "-c"}
		for i := len(subvols) - 1; i >= 0; i-- {
			deleteArgs = append(deleteArgs, subvols[i].Path)
		}

		logrus.WithField("subvols", subvols).Debug("deleting subvolumes")
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

func (b *btrfsOps) Name() string {
	return "btrfs"
}

var _ FSOps = &btrfsOps{}
