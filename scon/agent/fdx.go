package agent

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

type Fdx struct {
	conn *net.UnixConn
}

func NewFdx(conn net.Conn) *Fdx {
	return &Fdx{
		conn: conn.(*net.UnixConn),
	}
}

func (f *Fdx) Close() error {
	return f.conn.Close()
}

func (f *Fdx) SendFdInt(fd int) error {
	oob := unix.UnixRights(fd)
	_, oobn, err := f.conn.WriteMsgUnix(nil, unix.UnixRights(fd), nil)
	if err != nil {
		return err
	}
	if oobn != len(oob) {
		return errors.New("short write")
	}
	return nil
}

func (f *Fdx) RecvFdInt() (int, error) {
	oob := make([]byte, unix.CmsgSpace(4))
	// use f.conn.ReadMsgUnix
	_, oobn, _, _, err := f.conn.ReadMsgUnix(nil, oob)
	if err != nil {
		return -1, err
	}
	if oobn != len(oob) {
		return -1, errors.New("short read")
	}
	scms, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return -1, err
	}
	if len(scms) != 1 {
		return -1, errors.New("unexpected number of socket control messages")
	}
	// cloexec safe: Go sets MSG_CMSG_CLOEXEC
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		return -1, err
	}
	if len(fds) != 1 {
		return -1, errors.New("unexpected number of file descriptors")
	}
	return fds[0], nil
}

func (f *Fdx) SendFile(file *os.File) error {
	return f.SendFdInt(int(file.Fd()))
}

func (f *Fdx) RecvFile() (*os.File, error) {
	fd, err := f.RecvFdInt()
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "fdx"), nil
}
