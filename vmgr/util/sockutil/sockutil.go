package sockutil

import (
	"fmt"
	"net"
	"os"
	"sync"

	sconutil "github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// https://github.com/apple-oss-distributions/xnu/blob/e3723e1f17661b24996789d8afc084c0c3303b26/bsd/netinet/tcp_private.h#L77
const cTCP_PEER_PID = 0x204

func getTCPPeerPID(conn *net.TCPConn) (int, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}

	peerPid, err := sconutil.UseRawConn1(rawConn, func(fd int) (int, error) {
		return unix.GetsockoptInt(fd, unix.IPPROTO_TCP, cTCP_PEER_PID)
	})
	if err != nil {
		return 0, fmt.Errorf("getsockopt: %w", err)
	}

	return peerPid, nil
}

// security critical, so we should test to make sure the private API is working as expected
// non-netx ok: very short test; disabling keepalive is a waste of syscalls
var testTCPPeerPID = sync.OnceValue(func() error {
	// create a localhost listener
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	// accept one connection
	srvConnCh := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			logrus.WithError(err).Error("failed to accept connection for pid test")
			return
		}
		srvConnCh <- conn
	}()

	// connect to it
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// wait for the server to accept the connection
	srvConn := <-srvConnCh
	defer srvConn.Close()

	// get the peer pid from server POV (since that's what we use it for in NFS)
	peerPid, err := getTCPPeerPID(srvConn.(*net.TCPConn))
	if err != nil {
		return fmt.Errorf("get pid: %w", err)
	}

	// it should match our pid
	if peerPid != os.Getpid() {
		return fmt.Errorf("pid mismatch: expected %d, got %d", os.Getpid(), peerPid)
	}

	return nil
})

func GetTCPPeerPID(conn *net.TCPConn) (int, error) {
	err := testTCPPeerPID()
	if err != nil {
		return 0, fmt.Errorf("test: %w", err)
	}

	return getTCPPeerPID(conn)
}
