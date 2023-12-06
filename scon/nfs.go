package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	nfsDirRoot           = "/nfs/root"
	nfsDirContainers     = "/nfs/containers"
	nfsDirContainersFuse = "/nfs/containers-export"
	nfsDirForMachines    = "/nfs/for-machines"

	fstypeFuseBind = "fbind"

	nfsExportsDebounce = 250 * time.Millisecond
)

type NfsMirrorManager struct {
	roDir string
	rwDir string

	mu    sync.Mutex
	dests map[string]nfsMountEntry

	nextFsid        int
	hostUid         int
	exportsDebounce syncx.FuncDebounce
	controlsExports bool
}

type nfsMountEntry struct {
	Fsid int
}

func newNfsMirror(dir string, controlsExports bool) *NfsMirrorManager {
	m := &NfsMirrorManager{
		roDir:           dir + "/ro/",
		rwDir:           dir + "/rw/",
		dests:           make(map[string]nfsMountEntry),
		nextFsid:        100,
		controlsExports: controlsExports,
	}

	m.exportsDebounce = syncx.NewFuncDebounce(nfsExportsDebounce, func() {
		err := m.updateExports()
		if err != nil {
			logrus.WithError(err).Error("failed to update exports")
		}
	})

	return m
}

func (m *NfsMirrorManager) Mount(source string, subdest string, fstype string, flags uintptr, data string, mountFd int) error {
	// special case for FUSE bind mount for nfs containers-export
	needsExports := fstype != "" // typically for overlay or fuse
	if fstype == fstypeFuseBind {
		// FUSE bind mount does also need fsid
		needsExports = true
		fstype = ""
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	backingPath := m.rwDir + subdest
	destPath := m.roDir + subdest

	if verboseDebug {
		logrus.WithFields(logrus.Fields{
			"src": source,
			"dst": destPath,
		}).Debug("mounting nfs dir")
	}
	err := os.MkdirAll(backingPath, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("mkdir %s: %w", backingPath, err)
	}

	// unmount first
	// MNT_DETACH is recursive
	_ = unix.Unmount(destPath, unix.MNT_DETACH)

	// bind mount
	if mountFd == -1 {
		err = unix.Mount(source, destPath, fstype, flags, data)
		if err != nil {
			return fmt.Errorf("mount %s: %w", destPath, err)
		}
	} else {
		err = unix.MoveMount(mountFd, "", unix.AT_FDCWD, destPath, unix.MOVE_MOUNT_F_EMPTY_PATH)
		if err != nil {
			return fmt.Errorf("move mount %s: %w", destPath, err)
		}

		// make rprivate to prevent unmounts from propagating
		// otherwise it breaks kind, which uses systemd, which remounts all as shared
		err = unix.Mount("", destPath, "", unix.MS_REC|unix.MS_PRIVATE, "")
		if err != nil {
			return fmt.Errorf("remount %s: %w", destPath, err)
		}

		// this is a recursive mount (open_tree was called with AT_RECURSIVE)
		// now unmount undesired /proc, /dev, /sys recursively
		// too many files and not very useful
		err = unix.Unmount(destPath+"/proc", unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) {
			// EINVAL = not mounted
			return fmt.Errorf("unmount %s/p: %w", destPath, err)
		}
		err = unix.Unmount(destPath+"/dev", unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) {
			// EINVAL = not mounted
			return fmt.Errorf("unmount %s/d: %w", destPath, err)
		}
		err = unix.Unmount(destPath+"/sys", unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) {
			// EINVAL = not mounted
			return fmt.Errorf("unmount %s/s: %w", destPath, err)
		}
	}

	// fsid is only needed for overlay and fuse (non-bind mounts)
	var entry nfsMountEntry
	if needsExports && m.controlsExports {
		m.nextFsid++
		entry = nfsMountEntry{
			Fsid: m.nextFsid,
		}
		m.exportsDebounce.Call()
	}
	m.dests[destPath] = entry
	return nil
}

func (m *NfsMirrorManager) MountBind(source string, subdest string) error {
	return m.Mount(source, subdest, "", unix.MS_BIND, "", -1)
}

func (m *NfsMirrorManager) Unmount(subdest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.unmountLocked(subdest)
}

func (m *NfsMirrorManager) unmountLocked(subdest string) error {
	backingPath := m.rwDir + subdest
	mountPath := m.roDir + subdest

	entry, ok := m.dests[mountPath]
	if !ok {
		return nil
	}

	if verboseDebug {
		logrus.WithField("dst", mountPath).Trace("unmounting nfs dir")
	}
	// unmount
	// MNT_DETACH is recursive
	err := unix.Unmount(mountPath, unix.MNT_DETACH)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		// EINVAL = not mounted
		return err
	}

	// remove directory
	err = os.Remove(backingPath)
	if err != nil {
		return err
	}

	delete(m.dests, mountPath)
	if entry.Fsid != 0 {
		m.exportsDebounce.Call()
	}
	return nil
}

func (m *NfsMirrorManager) UnmountAll(prefix string) error {
	var errs []error
	for dest := range m.dests {
		subdest := strings.TrimPrefix(dest, m.roDir)
		if !strings.HasPrefix(subdest, prefix) {
			continue
		}

		err := m.unmountLocked(subdest)
		if err != nil {
			errs = append(errs, fmt.Errorf("unmount %s: %w", dest, err))
		}
	}

	return errors.Join(errs...)
}

func (m *NfsMirrorManager) Close() error {
	return m.UnmountAll("")
}

func (m *ConManager) onRestoreContainer(c *Container) error {
	// nfs bind mount
	err := func() error {
		// docker is special
		if c.ID == ContainerIDDocker {
			return nil
		}

		err := m.nfsForAll.MountBind(c.rootfsDir, c.Name)
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) onPreDeleteContainer(c *Container) error {
	// nfs symlink
	err := func() error {
		// docker is special
		if c.ID == ContainerIDDocker {
			return nil
		}

		err := m.nfsForAll.Unmount(c.Name)
		if err != nil {
			return err
		}

		return nil
	}()
	if err != nil {
		return err
	}

	return nil
}

func bindMountNfsRoot(c *Container, src string, target string) error {
	return c.UseMountNs(func() error {
		// shared from machine POV is OK
		return unix.Mount(src, target, "", unix.MS_BIND|unix.MS_REC|unix.MS_SHARED|unix.MS_RDONLY, "")
	})
}

func (m *NfsMirrorManager) MountImage(img *dockertypes.FullImage, tag string, fs *securefs.FS) error {
	// c8d snapshotter not supported
	if img.GraphDriver.Name != "overlay2" {
		return nil
	}

	// open each dir as O_PATH fd. layer paths are too long so normally docker uses symlinks
	// TODO use proc root fd
	lowerDirValue := img.GraphDriver.Data["LowerDir"]
	lowerParts := strings.Split(lowerDirValue, ":")
	// make it empty?
	if lowerDirValue == "" {
		lowerParts = nil
	}
	layerDirs := make([]string, 0, 1+len(img.GraphDriver.Data))
	// upper first
	upperPath := strings.TrimPrefix(img.GraphDriver.Data["UpperDir"], "/var/lib/docker")
	// an image should never have no layers
	if upperPath == "" {
		return fmt.Errorf("image '%s' has no upper dir", tag)
	}

	// scope to securefs
	upperFd, err := fs.OpenFd(upperPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open upper dir '%s': %w", upperPath, err)
	}
	defer unix.Close(upperFd)
	// upper first, by order
	layerDirs = append(layerDirs, "/proc/self/fd/"+strconv.Itoa(upperFd))

	for _, dir := range lowerParts {
		lowerPath := strings.TrimPrefix(dir, "/var/lib/docker")
		lowerFd, err := fs.OpenFd(lowerPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open lower dir '%s': %w", lowerPath, err)
		}
		defer unix.Close(lowerFd)
		layerDirs = append(layerDirs, "/proc/self/fd/"+strconv.Itoa(lowerFd))
	}

	// overlayfs does not support having only a single lowerdir. use same code path
	if len(layerDirs) == 1 {
		layerDirs = append(layerDirs, "/tmp/empty")
	}

	subDest := "docker/images/" + tag
	err = m.Mount("img", subDest, "overlay", unix.MS_RDONLY, "redirect_dir=nofollow,nfs_export=on,lowerdir="+strings.Join(layerDirs, ":"), -1)
	if err != nil {
		return fmt.Errorf("mount overlay on %s: %w", subDest, err)
	}

	return nil
}

func (m *NfsMirrorManager) updateExports() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 127.0.0.8 = vsock
	// root export needs to be rw for machines
	// docker/volumes export has different uid/gid
	exportsBase := fmt.Sprintf(`
/nfs/root/ro 127.0.0.8(rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check,anonuid=%d,anongid=%d)
/nfs/root/ro/docker/volumes 127.0.0.8(rw,async,fsid=1,crossmnt,insecure,all_squash,no_subtree_check,anonuid=0,anongid=0)
/nfs/root/ro/docker/containers 127.0.0.8(rw,async,fsid=2,crossmnt,insecure,all_squash,no_subtree_check,anonuid=0,anongid=0)
`, m.hostUid, m.hostUid)

	destLines := make([]string, 0, len(m.dests))
	for path, entry := range m.dests {
		if entry.Fsid == 0 {
			// doesn't need fsid, handled by mount propagation
			continue
		}

		destLines = append(destLines, fmt.Sprintf("%s 127.0.0.8(ro,async,fsid=%d,crossmnt,insecure,all_squash,no_subtree_check,anonuid=0,anongid=0)", path, entry.Fsid))
	}
	exportsBase += strings.Join(destLines, "\n")

	err := os.WriteFile(conf.C().EtcExports, []byte(exportsBase), 0644)
	if err != nil {
		return err
	}

	// can't write directly to /proc because etab needs to be written for rpc.mountd
	err = util.Run("/opt/pkg/exportfs", "-ar")
	if err != nil {
		return err
	}

	return nil
}
