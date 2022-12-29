package conf

import (
	"os/exec"
	"strconv"
)

var (
	// mutejukebox = don't show "fs not responding" dialog
	// rwsize=131072,readahead=64 optimal for vsock
	nfsMountOptions = "vers=4,tcp,inet,port=" + strconv.Itoa(HostPortNFS) + ",soft,mutejukebox,rwsize=131072,readahead=64"
)

func MountNfs() error {
	err := exec.Command("mount", "-t", "nfs", "-o", nfsMountOptions, "127.0.0.1:", NfsMountpoint()).Run()
	if err != nil {
		return err
	}

	return nil
}

func UnmountNfs() error {
	err := exec.Command("umount", "-f", NfsMountpoint()).Run()
	if err != nil {
		return err
	}

	return nil
}
