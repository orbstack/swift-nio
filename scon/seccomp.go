package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"

	"github.com/lxc/go-lxc"
	seccomp "github.com/seccomp/libseccomp-golang"
	"golang.org/x/sys/unix"
)

func rescanListeners(c *lxc.Container) error {
	// TODO
	return nil
}

func monitorSeccompNotifier(c *lxc.Container, sfd seccomp.ScmpFd) error {
	for {
		req, err := seccomp.NotifReceive(sfd)
		if err != nil {
			return err
		}
		fmt.Println("RRRR req", req)

		resp := seccomp.ScmpNotifResp{
			ID:    req.ID,
			Error: 0,
			Val:   0,
			Flags: seccomp.NotifRespFlagContinue,
		}
		err = seccomp.NotifRespond(sfd, &resp)
		if err != nil {
			return err
		}

		go rescanListeners(c)
	}
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
	fmt.Println("n in", n)
	fmt.Println("b64 in", base64.StdEncoding.EncodeToString(buf[:n]))
	fmt.Println("hex in", hex.Dump(buf[:n]))

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
	_ = os.Remove("/tmp/seccomp.sock")
	listener, err := net.Listen("unixpacket", "/tmp/seccomp.sock")
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
					Flags: seccomp.NotifRespFlagContinue,
				}

				var buf bytes.Buffer
				err = binary.Write(&buf, binary.LittleEndian, msg)
				if err != nil {
					fmt.Println("write err", err)
					return
				}
				fmt.Println("n out", buf.Len())
				fmt.Println("b64 out", base64.StdEncoding.EncodeToString(buf.Bytes()))
				fmt.Println("hex out", hex.Dump(buf.Bytes()))

				_, err = conn.Write(buf.Bytes())
				if err != nil {
					fmt.Println("write err", err)
					return
				}
				fmt.Println("responded")
			}
		}(conn.(*net.UnixConn))
	}
}
