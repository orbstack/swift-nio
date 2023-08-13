package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	nfsDirRoot       = "/nfs/root"
	nfsDirImages     = "/nfs/images"
	nfsDirContainers = "/nfs/containers"
)

func mountOneNfs(source string, mirrorDir string, subDest string, fstype string, flags uintptr, data string) error {
	backingPath := mirrorDir + "/rw/" + subDest
	destPath := mirrorDir + "/ro/" + subDest

	logrus.WithFields(logrus.Fields{
		"src": source,
		"dst": destPath,
	}).Trace("mounting nfs dir")
	err := os.MkdirAll(backingPath, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}

	// unmount first
	err = unix.Unmount(destPath, unix.MNT_DETACH)
	if err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}

	// bind mount
	err = unix.Mount(source, destPath, fstype, flags, data)
	if err != nil {
		return err
	}

	return nil
}

func mountOneNfsBind(source string, mirrorDir string, nfsSubDst string) error {
	return mountOneNfs(source, mirrorDir, nfsSubDst, "", unix.MS_BIND, "")
}

func unmountOneNfs(mirrorDir string, nfsSubDst string) error {
	backingPath := mirrorDir + "/rw/" + nfsSubDst
	mountPath := mirrorDir + "/ro/" + nfsSubDst

	logrus.WithField("dst", mountPath).Debug("unmounting nfs dir")
	// unmount
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

	return nil
}

func (m *ConManager) onRestoreContainer(c *Container) error {
	// nfs bind mount
	err := func() error {
		m.nfsMu.Lock()
		defer m.nfsMu.Unlock()

		// docker is special
		if c.ID == ContainerIDDocker {
			return nil
		}

		err := mountOneNfsBind(c.rootfsDir, nfsDirRoot, c.Name)
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
		m.nfsMu.Lock()
		defer m.nfsMu.Unlock()

		// docker is special
		if c.ID == ContainerIDDocker {
			return nil
		}

		err := unmountOneNfs(nfsDirRoot, c.Name)
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
		return unix.Mount(src, target, "", unix.MS_BIND|unix.MS_REC|unix.MS_SHARED|unix.MS_RDONLY, "")
	})
}

func mountOneNfsImage(img *dockertypes.FullImage) error {
	// guaranteed that there's a tag at this point
	tag := img.RepoTags[0]

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
	upperPath := strings.Replace(img.GraphDriver.Data["UpperDir"], "/var/lib/docker", conf.C().DockerDataDir, 1)
	// an image should never have no layers
	if upperPath == "" {
		return fmt.Errorf("image '%s' has no upper dir", tag)
	}

	upperFd, err := unix.Open(upperPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open upper dir '%s': %w", upperPath, err)
	}
	defer unix.Close(upperFd)
	// upper first, by order
	layerDirs = append(layerDirs, "/proc/self/fd/"+strconv.Itoa(upperFd))

	for _, dir := range lowerParts {
		lowerPath := strings.Replace(dir, "/var/lib/docker", conf.C().DockerDataDir, 1)
		lowerFd, err := unix.Open(lowerPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open lower dir '%s': %w", lowerPath, err)
		}
		defer unix.Close(lowerFd)
		layerDirs = append(layerDirs, "/proc/self/fd/"+strconv.Itoa(lowerFd))
	}

	// overlayfs does not support having only a single lowerdir.
	// just use bind mount instead in that case, e.g. single-layer base image like alpine
	if len(layerDirs) == 1 {
		err = mountOneNfsBind(layerDirs[0], nfsDirImages, tag)
		if err != nil {
			return fmt.Errorf("mount bind: %w", err)
		}
	} else {
		// note: nfs_export not really needed because of mergerfs
		err = mountOneNfs("img", nfsDirImages, tag, "overlay", unix.MS_RDONLY, "redirect_dir=nofollow,nfs_export=on,lowerdir="+strings.Join(layerDirs, ":"))
		if err != nil {
			return fmt.Errorf("mount overlay: %w", err)
		}
	}

	return nil
}
