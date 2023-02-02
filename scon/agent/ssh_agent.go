package agent

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
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

	cmd := exec.Command("/opt/macvirt-guest/scon-forksftp")
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
