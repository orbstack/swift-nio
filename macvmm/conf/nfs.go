package conf

import (
	"os/exec"
	"strconv"
)

var (
	// mutejukebox = don't show "fs not responding" dialog
	nfsMountOptions = "vers=4,tcp,inet,port=" + strconv.Itoa(HostPortNFS) + ",soft,mutejukebox"
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
