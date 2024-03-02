package main

import (
	"io"
	"net"
	"os"
	"strconv"

	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"golang.org/x/sys/unix"
)

// this is in here instead of orbctl because we're the one writing ssh config
func runSshProxyFdpass() {
	// it's not valid to start vmgr as another user.
	// (e.g. when running as Nix build user)
	// in that case just fail if it's not already running
	expectedUid, err := strconv.Atoi(os.Args[2])
	check(err)
	if os.Getuid() != expectedUid {
		return
	}

	err = vmclient.EnsureSconVM()
	check(err)

	// don't bother to close anything, we're going to exit anyway

	// dial tcp; unix causes following error:
	// setsockopt TCP_NODELAY: Operation not supported on socket
	conn, err := netx.Dial("tcp", "127.0.0.1:"+str(ports.HostSconSSHPublic))
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

	// wait for ssh to receive the fd and close its side of the socketpair
	// needed to prevent race where fd is in SCM_RIGHTS but not yet received, and then we exit, and XNU closes the connection
	var data [1]byte
	_, err = sshSock.Read(data[:])
	if err != io.EOF {
		panic(err)
	}
}
