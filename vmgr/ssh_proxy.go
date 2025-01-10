package vmgr

import (
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"golang.org/x/sys/unix"
)

type pipeConn struct {
	r *os.File
	w *os.File
}

func (c *pipeConn) Read(b []byte) (n int, err error) {
	return c.r.Read(b)
}

func (c *pipeConn) Write(b []byte) (n int, err error) {
	return c.w.Write(b)
}

func (c *pipeConn) CloseWrite() error {
	return c.w.Close()
}

func (c *pipeConn) Close() error {
	c.r.Close()
	c.w.Close()
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr {
	return nil
}

func (c *pipeConn) RemoteAddr() net.Addr {
	return nil
}

func (c *pipeConn) SetDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (c *pipeConn) SetReadDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (c *pipeConn) SetWriteDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func sshProxySendFd(conn *net.TCPConn) {
	sshSock, err := net.FileConn(os.Stdout)
	check(err)

	// nonblock is ok, ssh sets it anyway
	rawConn, err := conn.SyscallConn()
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

func sshProxyPump(conn *net.TCPConn) {
	tcppump.Pump2(conn, &pipeConn{r: os.Stdin, w: os.Stdout})
}

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

	// if os.Stdout is not a socket, it means we're being invoked by a non-OpenSSH ssh client that doesn't support ProxyUseFdpass, but does support ProxyCommand
	// so stdout is a pipe and we're expected to proxy to it
	_, err = util.UseFile1(os.Stdout, func(fd int) (unix.Sockaddr, error) {
		return unix.Getsockname(fd)
	})
	if errors.Is(err, unix.ENOTSOCK) {
		// we have a pipe
		// proxy it
		sshProxyPump(conn.(*net.TCPConn))
	} else {
		// we have a socket
		// send fd
		sshProxySendFd(conn.(*net.TCPConn))
	}
}
