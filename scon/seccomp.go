package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"

	"github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"
)

const cNotifRespFlagContinue = 1

const seccompPolicy = `2
denylist
bind notify
`

func rescanListeners(c *lxc.Container) error {
	// TODO
	return nil
}

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
}

func readSeccompProxyMsg(conn *net.UnixConn) (*scmpNotifyProxyMsg, error) {
	buf := make([]byte, 1024)
	oob := make([]byte, 1024)
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

func runSeccompServer() error {
	_ = os.Remove(seccompProxySock)
	listener, err := net.Listen("unixpacket", seccompProxySock)
	if err != nil {
		fmt.Println("listen err", err)
		return err
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("accept err", err)
			return err
		}

		go func(conn *net.UnixConn) {
			defer conn.Close()

			for {
				msg, err := readSeccompProxyMsg(conn)
				if err != nil {
					fmt.Println("read err", err)
					return
				}

				fmt.Println("msg", msg)

				msg.Resp = scmpNotifResp{
					ID:    msg.Req.ID,
					Error: 0,
					Val:   0,
					Flags: cNotifRespFlagContinue,
				}

				var buf bytes.Buffer
				err = binary.Write(&buf, binary.LittleEndian, msg)
				if err != nil {
					fmt.Println("write err", err)
					return
				}

				_, err = conn.Write(buf.Bytes())
				if err != nil {
					fmt.Println("write err", err)
					return
				}

				go rescanListeners(nil)
			}
		}(conn.(*net.UnixConn))
	}
}
