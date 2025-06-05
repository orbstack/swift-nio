package nfsmnt

import (
	"errors"
	"fmt"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"golang.org/x/sys/unix"
)

// TODO: add /OrbStack on server side, and use FS_LOCATIONS to mount the subdir. fake MNTFROM (f_mntfromname) doesn't work with AF_LOCAL because it gets overwritten late: https://github.com/apple-oss-distributions/NFS/blob/b773791769ec981ca1400c137a06af7678b87034/kext/nfs_vfsops.c#L3307
const preferUnixSocket = false

func MountNfs(tcpPort int) error {
	// similar to:
	// mount_nfs -vvvvv -o vers=4,proto=ticotsord,soft,intr,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback "<$HOME/.orbstack/run/nfs.sock>":/ ~/OrbStack
	// mount_nfs -vvvvv -o vers=4,tcp,inet,soft,intr,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback,port=62429 localhost: ~/OrbStack
	spec := Spec{
		TargetPath: coredir.EnsureNfsMountpoint(),
	}
	if tcpPort == 0 {
		spec.IsUnix = true
		spec.Addr = conf.NfsSocket()
	} else {
		spec.IsUnix = false
		spec.Addr = "127.0.0.1"
		spec.TcpPort = uint16(tcpPort)
	}

	err := doMount(spec)
	if err != nil {
		return fmt.Errorf("mount nfs: %w", err)
	}

	return nil
}

// use raw path (no stat/ensure dir) to prevent hang if broken
func UnmountNfs() error {
	err := unix.Unmount(coredir.NfsMountpoint(), unix.MNT_FORCE)
	// EINVAL is normal if not mounted
	// we can't check whether it's mounted because a stat could hang
	if err != nil && !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("unmount nfs: %w", err)
	}

	return nil
}
