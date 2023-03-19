package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	cNotifRespFlagContinue = 1
)

// TODO better fix for btrfs quota
const seccompPolicy = `2
denylist
bind notify
ioctl errno 1 [1,3222311976,SCMP_CMP_EQ]
init_module errno 38
finit_module errno 38
delete_module errno 38
`

type scmpNotifSizes struct {
	ScmpNotif     uint16
	ScmpNotifResp uint16
	ScmpData      uint16
}

type scmpData struct {
	Syscall int32
	//Arch         uint
	Arch         uint32
	InstrPointer uint64
	// args         [6]uint64
	Arg0 uint64
	Arg1 uint64
	Arg2 uint64
	Arg3 uint64
	Arg4 uint64
	Arg5 uint64
}

type scmpNotifReq struct {
	Padding uint16
	ID      uint64
	Pid     uint32
	Flags   uint32
	Data    scmpData
}

type scmpNotifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

type scmpNotifyProxyMsg struct {
	Reserved   uint64
	MonitorPid uint32
	InitPid    uint32
	Sizes      scmpNotifSizes
	CookieLen  uint64

	Req  scmpNotifReq
	Resp scmpNotifResp

	// plus our cookie
	Cookie uint64
}

func makeSeccompCookie() (string, uint64, error) {
	cookieBytes := make([]byte, 8)

	// fill with random bytes, but not 0
	n, err := rand.Read(cookieBytes)
	if err != nil {
		return "", 0, err
	}
	if n != 8 {
		return "", 0, errors.New("short read")
	}

	// make sure we don't have a 0 byte
	for i := 0; i < 8; i++ {
		cookieBytes[i] = cookieBytes[i] | 1
	}

	cookie := string(cookieBytes)
	cookieInt := binary.LittleEndian.Uint64(cookieBytes)
	return cookie, cookieInt, nil
}

func readSeccompProxyMsg(conn *net.UnixConn) (*scmpNotifyProxyMsg, error) {
	buf := make([]byte, 256)
	oob := make([]byte, 256)
	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, err
	}

	cmsgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return nil, err
	}

	if len(cmsgs) != 1 {
		return nil, fmt.Errorf("expected 1 cmsg, got %d", len(cmsgs))
	}

	// cloexec safe: Go sets MSG_CMSG_CLOEXEC
	fds, err := unix.ParseUnixRights(&cmsgs[0])
	if err != nil {
		return nil, err
	}

	if len(fds) != 3 {
		return nil, fmt.Errorf("expected 3 fds, got %d", len(fds))
	}

	procF := os.NewFile(uintptr(fds[0]), "proc")
	memF := os.NewFile(uintptr(fds[1]), "mem")
	notifyF := os.NewFile(uintptr(fds[2]), "notify")
	defer procF.Close()
	defer memF.Close()
	defer notifyF.Close()

	// parse scmpNotifyProxyMsg
	msg := &scmpNotifyProxyMsg{}
	buf = buf[:n]
	err = binary.Read(bytes.NewReader(buf), binary.LittleEndian, msg)
	if err != nil {
		return nil, err
	}

	return msg, nil
}

func (m *ConManager) handleSeccompMsg(msg *scmpNotifyProxyMsg) {
	cookie := msg.Cookie
	m.containersMu.RLock()
	container, ok := m.seccompCookies[msg.Cookie]
	m.containersMu.RUnlock()
	if !ok {
		logrus.Error("seccomp cookie not found: ", cookie)
		return
	}

	container.triggerListenersUpdate()
}

func (m *ConManager) serveSeccomp() error {
	listener, err := net.Listen("unixpacket", m.seccompProxySock)
	if err != nil {
		logrus.Error("seccomp listen: ", err)
		return err
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}

		go func(conn *net.UnixConn) {
			defer conn.Close()

			for {
				msg, err := readSeccompProxyMsg(conn)
				if err != nil {
					return
				}

				msg.Resp.Flags = cNotifRespFlagContinue

				var buf bytes.Buffer
				err = binary.Write(&buf, binary.LittleEndian, msg)
				if err != nil {
					logrus.Error("seccomp write: ", err)
					return
				}
				// strip cookie in reply
				reply := buf.Bytes()[:buf.Len()-8]

				_, err = conn.Write(reply)
				if err != nil {
					logrus.Error("seccomp write: ", err)
					return
				}

				go m.handleSeccompMsg(msg)
			}
		}(conn.(*net.UnixConn))
	}
}
