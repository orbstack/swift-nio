package cmd

import (
	"fmt"
	"net"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func init() {
	internalCmd.AddCommand(internalSshProxyFdpassCmd)
}

var internalSshProxyFdpassCmd = &cobra.Command{
	Use:    "ssh-proxy-fdpass",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		vmclient.EnsureSconVM()

		// don't bother to close anything, we're going to exit anyway

		// dial unix
		conn, err := net.Dial("unix", conf.SconSSHSocket())
		checkCLI(err)

		fmt.Fprintln(os.Stderr, "test out")

		// send fd
		sshSock, err := net.FileConn(os.Stdout)
		checkCLI(err)

		rawConn, err := conn.(*net.UnixConn).SyscallConn()
		checkCLI(err)
		rawConn.Control(func(fd uintptr) {
			oob := unix.UnixRights(int(fd))
			n, oobn, err := sshSock.(*net.UnixConn).WriteMsgUnix(nil, oob, nil)
			checkCLI(err)
			if n != 1 || oobn != len(oob) {
				panic("failed to send fd")
			}
		})

		return nil
	},
}
