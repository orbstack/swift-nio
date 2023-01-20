package conf

import (
	"os/exec"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"golang.org/x/sys/unix"
)

var (
	// mutejukebox = don't show "fs not responding" dialog
	// rwsize=131072,readahead=64 optimal for vsock
	nfsMountOptions = "vers=4,tcp,inet,port=" + strconv.Itoa(ports.HostNFS) + ",soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10"
)

func MountNfs() error {
	err := exec.Command("mount", "-t", "nfs", "-o", nfsMountOptions, "127.0.0.1:", NfsMountpoint()).Run()
	if err != nil {
		return err
	}

	return nil
}

func UnmountNfs() error {
	return unix.Unmount(NfsMountpoint(), unix.MNT_FORCE)
}
