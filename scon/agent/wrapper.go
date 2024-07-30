package agent

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func socketpair(typ int) (file0 *os.File, conn1 net.Conn, err error) {
	// cloexec safe
	fds, err := unix.Socketpair(unix.AF_UNIX, typ|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return
	}

	file0 = os.NewFile(uintptr(fds[0]), "socketpair0")
	file1 := os.NewFile(uintptr(fds[1]), "socketpair1")
	defer file1.Close()
	conn1, err = net.FileConn(file1)
	if err != nil {
		file0.Close()
		return
	}

	return
}

func MakeAgentFds() (*os.File, *os.File, net.Conn, net.Conn, error) {
	// 1. rpc socket
	rpcFile, rpcConn, err := socketpair(unix.SOCK_STREAM)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// 2. unix socket to send fds
	fdxFile, fdxConn, err := socketpair(unix.SOCK_DGRAM)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return rpcFile, fdxFile, rpcConn, fdxConn, nil
}
