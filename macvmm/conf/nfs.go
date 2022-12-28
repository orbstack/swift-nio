package conf

import (
	"os/exec"
	"strconv"
)

var (
	nfsMountOptions = "vers=4,tcp,inet,port=" + strconv.Itoa(HostPortNFS) + ",soft"
)

func MountNfs() error {
	err := exec.Command("mount", "-t", "nfs", "-o", nfsMountOptions, "127.0.0.1:", GetNfsMountDir()).Run()
	if err != nil {
		return err
	}

	return nil
}

func UnmountNfs() error {
	err := exec.Command("umount", "-f", GetNfsMountDir()).Run()
	if err != nil {
		return err
	}

	return nil
}
