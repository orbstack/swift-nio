package nfsmnt

import (
	"fmt"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func MountNfs() error {
	logrus.Debug("mounting nfs")
	// similar to:
	// mount_nfs -vvvvv -o vers=4,proto=ticotsord,soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback "<$HOME/.orbstack/run/nfs.sock>":/ ~/OrbStack
	// mount_nfs -vvvvv -o vers=4,tcp,inet,soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback,port=62429 localhost: ~/OrbStack
	err := doMount(Spec{
		// IsUnix:  true,
		// Addr:    NfsSocket(),
		// TcpPort: 0,
		IsUnix:     false,
		Addr:       "127.0.0.1",
		TcpPort:    ports.HostNFS,
		TargetPath: conf.NfsMountpoint(),
	})
	if err != nil {
		return fmt.Errorf("mount nfs: %w", err)
	}

	return nil
}

func UnmountNfs() error {
	return unix.Unmount(conf.NfsMountpoint(), unix.MNT_FORCE)
}
