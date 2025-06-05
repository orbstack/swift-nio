package nfsmnt

import (
	"errors"
	"os"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/sirupsen/logrus"
)

const unmountTimeout = 10 * time.Second

const (
	readmeText = `# OrbStack file sharing

When OrbStack is running, this folder contains containers, images, volumes, and machines. All Docker and Linux files can be found here.

This is only a *view* of OrbStack's data; it takes no space on disk, and data is not actually stored here. The default data location is: ~/Library/Group Containers/HUAQ24HBR6.dev.orbstack/data

This folder is empty when OrbStack is not running. Do not put files here.

Learn more:
    - https://orb.cx/orbstack-folder
    - https://orb.cx/docker-mount
    - https://orb.cx/machine-mount


## Docker

OrbStack uses standard Docker named volumes.

Create a volume: ` + "`" + `docker volume create foo` + "`" + `
Mount into a container: ` + "`" + `docker run -v foo:/bar ...` + "`" + `
    - Use the volume name to mount it. DO NOT use ~/OrbStack here!
See files from Mac: ` + "`" + `open ~/OrbStack/docker/volumes/foo` + "`" + `


---

[OrbStack is currently STOPPED. Files are NOT available.]
`
)

type Mounter struct {
	// 0 = use unix socket
	port    int
	mounted bool
}

func NewMounter(port int) *Mounter {
	return &Mounter{port: port}
}

func (m *Mounter) Mount() error {
	if m.mounted {
		return nil
	}

	// prep: create nfs dir, write readme, make read-only
	dir := coredir.EnsureNfsMountpoint()
	// coredir.NfsMountpoint() already calls mkdir
	err := util.WriteFileIfChanged(dir+"/README.txt", []byte(readmeText), 0644)
	// permission error is normal, that means it's already read only
	if err != nil && !errors.Is(err, os.ErrPermission) {
		logrus.WithError(err).Error("failed to write NFS readme")
	}
	err = os.Chmod(dir, 0555)
	if err != nil {
		logrus.WithError(err).Error("failed to chmod NFS dir")
	}

	logrus.Info("Mounting NFS...")
	err = MountNfs(m.port)
	if err != nil {
		logrus.WithError(err).Error("NFS mount failed")
		return err
	}

	logrus.Info("NFS mounted")
	m.mounted = true
	return nil
}

func (m *Mounter) Unmount() error {
	if !m.mounted {
		return nil
	}

	// force unmounting NFS always works on macOS, even if files are open
	logrus.Info("Unmounting NFS...")
	err := util.WithTimeout1(func() error {
		return UnmountNfs()
	}, unmountTimeout)
	if err != nil {
		logrus.WithError(err).Error("NFS unmount failed")
		return err
	}

	logrus.Info("NFS unmounted")
	m.mounted = false
	return nil
}
