package conf

import (
	"fmt"
	"os/exec"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	// mutejukebox = don't show "fs not responding" dialog
	// rwsize=131072,readahead=64 optimal for vsock
	nfsMountOptions = "vers=4,tcp,inet,port=" + strconv.Itoa(ports.HostNFS) + ",soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10"
)

func MountNfs() error {
	cmd := exec.Command("mount", "-t", "nfs", "-o", nfsMountOptions, "localhost:", NfsMountpoint())
	logrus.WithField("cmd", cmd.Args).Debug("mounting nfs")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount nfs: %w\n%s", err, output)
	}

	return nil
}

func UnmountNfs() error {
	return unix.Unmount(NfsMountpoint(), unix.MNT_FORCE)
}
