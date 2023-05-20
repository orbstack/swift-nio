package nfsmnt

import (
	"fmt"

	"github.com/orbstack/macvirt/macvmgr/conf/coredir"
	"golang.org/x/sys/unix"
)

func MountNfs(tcpPort int) error {
	// similar to:
	// mount_nfs -vvvvv -o vers=4,proto=ticotsord,soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback "<$HOME/.orbstack/run/nfs.sock>":/ ~/OrbStack
	// mount_nfs -vvvvv -o vers=4,tcp,inet,soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback,port=62429 localhost: ~/OrbStack
	err := doMount(Spec{
		// IsUnix:  true,
		// Addr:    NfsSocket(),
		// TcpPort: 0,
		IsUnix:     false,
		Addr:       "127.0.0.1",
		TcpPort:    uint16(tcpPort),
		TargetPath: coredir.NfsMountpoint(),
	})
	if err != nil {
		return fmt.Errorf("mount nfs: %w", err)
	}

	return nil
}

func UnmountNfs() error {
	return unix.Unmount(coredir.NfsMountpoint(), unix.MNT_FORCE)
}
