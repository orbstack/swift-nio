package conf

import (
	"fmt"
	"os/exec"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	// proto=ticotsord: SOCK_STREAM unix socket
	// mutejukebox = don't show "fs not responding" dialog
	// rwsize=131072,readahead=64 optimal for vsock
	// nocallback - fix nfs client id EINVAL with unix socket. callbacks are unused anyway - they're for delegation handoff with multiple clients
	nfsMountOptions = "vers=4,proto=ticotsord,soft,mutejukebox,rwsize=131072,readahead=64,deadtimeout=10,nocallback"
)

func MountNfs() error {
	cmd := exec.Command("mount", "-t", "nfs", "-o", nfsMountOptions, "<"+NfsSocket()+">:", NfsMountpoint())
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
