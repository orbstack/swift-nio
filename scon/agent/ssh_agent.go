package agent

import (
	"context"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
	"time"
)

const (
	dialTimeout = 15 * time.Second
)

type ServeSftpArgs struct {
	User   string
	FdxSeq uint64
}

func (a *AgentServer) ServeSftp(args *ServeSftpArgs, reply *int) error {
	socketFile, err := a.fdx.RecvFile(args.FdxSeq)
	if err != nil {
		return err
	}
	defer socketFile.Close()

	cmd := exec.Command("/opt/orbstack-guest/scon-forksftp")
	cmd.Stdin = socketFile
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// get user info
	u, err := user.Lookup(args.User)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	groupStrs, err := u.GroupIds()
	if err != nil {
		return err
	}
	groups := make([]uint32, len(groupStrs))
	for i, groupStr := range groupStrs {
		group, err := strconv.Atoi(groupStr)
		if err != nil {
			return err
		}
		groups[i] = uint32(group)
	}

	cmd.Dir = u.HomeDir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: groups,
		},
	}

	err = cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			*reply = exitErr.ExitCode()
			return nil
		}
		return err
	}

	*reply = cmd.ProcessState.ExitCode()
	return nil
}

type DialTCPContextArgs struct {
	AddrPort string
	//TODO signal rpc
}

func (a *AgentServer) DialTCPContext(args *DialTCPContextArgs, reply *uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", args.AddrPort)
	if err != nil {
		return err
	}
	defer conn.Close()

	file, err := conn.(*net.TCPConn).File()
	if err != nil {
		return err
	}
	defer file.Close()

	// send fd
	fdxSeq, err := a.fdx.SendFile(file)
	if err != nil {
		return err
	}

	*reply = fdxSeq
	return nil
}
