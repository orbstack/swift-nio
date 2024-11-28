// simple SCTP library, because github.com/ishidawataru/sctpv is not cloexec-safe and doesn't use nonblock

package sctplib

import (
	"errors"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type SCTPListener struct {
	f       *os.File
	rawConn syscall.RawConn
}

type SCTPAddr struct {
	Addr net.IP
	Port int
}

func ListenSCTP(addr *SCTPAddr) (*SCTPListener, error) {
	// SEQPACKET is like UDP unbound (one-to-many socket); STREAM is connection-oriented (one-to-one)
	ip4 := addr.Addr.To4()
	family := unix.AF_INET
	if ip4 == nil {
		family = unix.AF_INET6
	}
	sfd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_SCTP)
	if err != nil {
		return nil, err
	}

	// if IPv6, disable sctp46
	if ip4 == nil {
		err = unix.SetsockoptInt(sfd, unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 1)
		if err != nil {
			unix.Close(sfd)
			return nil, err
		}
	}

	// bind
	if ip4 == nil {
		err = unix.Bind(sfd, &unix.SockaddrInet6{Port: addr.Port, Addr: [16]byte(addr.Addr)})
	} else {
		err = unix.Bind(sfd, &unix.SockaddrInet4{Port: addr.Port, Addr: [4]byte(ip4)})
	}
	if err != nil {
		unix.Close(sfd)
		return nil, err
	}

	// listen
	err = unix.Listen(sfd, unix.SOMAXCONN)
	if err != nil {
		unix.Close(sfd)
		return nil, err
	}

	f := os.NewFile(uintptr(sfd), "sctp listener")
	rawConn, err := f.SyscallConn()
	if err != nil {
		f.Close()
		return nil, err
	}

	return &SCTPListener{
		f:       f,
		rawConn: rawConn,
	}, nil
}

func (l *SCTPListener) Accept() (net.Conn, error) {
	logrus.Debug("accepting sctp")
	var cfd int
	var err2 error
	err := l.rawConn.Read(func(fd uintptr) bool {
		cfd, _, err2 = unix.Accept4(int(fd), unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK)
		return err2 != unix.EWOULDBLOCK
	})
	if err != nil {
		return nil, err
	}
	if err2 != nil {
		return nil, err2
	}

	return &SCTPConn{
		f: os.NewFile(uintptr(cfd), "sctp conn"),
	}, nil
}

// TODO
func (l *SCTPListener) Addr() net.Addr {
	return nil
}

func (l *SCTPListener) Close() error {
	return l.f.Close()
}

type SCTPConn struct {
	f *os.File
}

func (c *SCTPConn) Read(b []byte) (int, error) {
	return c.f.Read(b)
}

func (c *SCTPConn) Write(b []byte) (int, error) {
	return c.f.Write(b)
}

// TODO
func (c *SCTPConn) LocalAddr() net.Addr {
	return nil
}

func (c *SCTPConn) RemoteAddr() net.Addr {
	return nil
}

func (c *SCTPConn) SetDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (c *SCTPConn) SetReadDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (c *SCTPConn) SetWriteDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (c *SCTPConn) Close() error {
	return c.f.Close()
}

func DialSCTP(raddr *SCTPAddr) (retConn *SCTPConn, retErr error) {
	ip4 := raddr.Addr.To4()
	family := unix.AF_INET
	if ip4 == nil {
		family = unix.AF_INET6
	}
	sfd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_SCTP)
	if err != nil {
		return nil, err
	}

	// turn into file
	f := os.NewFile(uintptr(sfd), "sctp conn")
	defer func() {
		if retErr != nil {
			f.Close()
		}
	}()

	// start connect
	if ip4 == nil {
		err = unix.Connect(sfd, &unix.SockaddrInet6{Port: raddr.Port, Addr: [16]byte(raddr.Addr)})
	} else {
		err = unix.Connect(sfd, &unix.SockaddrInet4{Port: raddr.Port, Addr: [4]byte(ip4)})
	}
	if err != nil && !errors.Is(err, unix.EINPROGRESS) {
		return nil, err
	}

	rawConn, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}

	// wait and get error
	var errValue int
	var err2 error
	err = rawConn.Write(func(fd uintptr) (done bool) {
		errValue, err2 = unix.GetsockoptInt(sfd, unix.SOL_SOCKET, unix.SO_ERROR)
		return unix.Errno(errValue) != unix.EINPROGRESS
	})
	if err != nil {
		return nil, err
	}
	if err2 != nil {
		return nil, err2
	}
	if errValue != 0 {
		return nil, unix.Errno(errValue)
	}

	return &SCTPConn{f: f}, nil
}
