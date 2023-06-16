package main

import (
	"net"
	"os"

	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/macvmgr/vmclient"
	"golang.org/x/sys/unix"
)

// this is in here instead of orbctl because we're the one writing ssh config
func runSshProxyFdpass() {
	err := vmclient.EnsureSconVM()
	check(err)

	// don't bother to close anything, we're going to exit anyway

	// dial tcp; unix causes following error:
	// setsockopt TCP_NODELAY: Operation not supported on socket
	conn, err := net.Dial("tcp", "127.0.0.1:"+str(ports.HostSconSSHPublic))
	check(err)

	// send fd
	sshSock, err := net.FileConn(os.Stdout)
	check(err)

	// nonblock is ok, ssh sets it anyway
	rawConn, err := conn.(*net.TCPConn).SyscallConn()
	check(err)
	err = rawConn.Control(func(fd uintptr) {
		oob := unix.UnixRights(int(fd))
		_, _, err := sshSock.(*net.UnixConn).WriteMsgUnix(nil, oob, nil)
		check(err)
	})
	check(err)

	os.Exit(0)
}
