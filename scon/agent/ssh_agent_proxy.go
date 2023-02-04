package agent

import (
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type SshAgentProxyArgs struct {
	Uid int
	Gid int
}

// this entire thing is a hacky workaround for a VS Code bug
// https://github.com/microsoft/vscode/issues/168202
func (a *AgentServer) StartSshAgentProxy(args *SshAgentProxyArgs, _ *None) error {
	// start ssh agent proxy
	go func() {
		err := RunSshAgentProxy(args)
		if err != nil {
			logrus.WithError(err).Error("ssh agent proxy exited with error")
		}
	}()

	return nil
}

func RunSshAgentProxy(args *SshAgentProxyArgs) error {
	os.Remove(mounts.TmpSshAgentProxySocket)
	listener, err := net.Listen("unix", mounts.TmpSshAgentProxySocket)
	if err != nil {
		return err
	}

	// set socket permissions
	err = os.Chmod(mounts.TmpSshAgentProxySocket, 0600)
	if err != nil {
		return err
	}
	err = os.Chown(mounts.TmpSshAgentProxySocket, args.Uid, args.Gid)
	if err != nil {
		return err
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}

		go func() {
			err := handleSshAgentProxyConn(conn.(*net.UnixConn))
			if err != nil {
				logrus.WithError(err).Error("failed to handle ssh agent proxy connection")
			}
		}()
	}
}

func readProcEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// parse environment
	env := make(map[string]string)
	for _, line := range strings.Split(string(data), "\x00") {
		if line == "" {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		env[key] = value
	}

	return env, nil
}

func handleSshAgentProxyConn(conn *net.UnixConn) error {
	defer conn.Close()

	// get SO_PEERCRED
	fd := util.GetConnFd(conn)
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return err
	}

	// read its environment
	procPid := "/proc/" + strconv.FormatInt(int64(cred.Pid), 10)
	env, err := readProcEnv(procPid + "/environ")
	if err != nil {
		return err
	}

	// get SSH_AUTH_SOCK
	sockPath, ok := env["SSH_AUTH_SOCK"]
	if !ok {
		// we're not supposed to proxy this
		return nil
	}

	// should we proxy this?
	if strings.HasPrefix(path.Base(sockPath), "vscode-ssh-auth-sock-") {
		// fix it
		sockPath = mounts.SshAgentSocket
	}

	// resolve socket path relative to process cwd
	if !path.IsAbs(sockPath) {
		cwd, err := os.Readlink(procPid + "/cwd")
		if err != nil {
			return err
		}

		sockPath = path.Join(cwd, sockPath)
	}

	// check permissions
	if cred.Uid != 0 {
		var stat unix.Stat_t
		err = unix.Stat(sockPath, &stat)
		if err != nil {
			return err
		}

		isOwner := stat.Uid == cred.Uid
		isGroupMember := (stat.Gid == cred.Gid) && !isOwner
		isOther := !isOwner && !isGroupMember

		// require both read and write
		allowsOwner := stat.Mode&unix.S_IRUSR != 0 && stat.Mode&unix.S_IWUSR != 0
		allowsGroup := stat.Mode&unix.S_IRGRP != 0 && stat.Mode&unix.S_IWGRP != 0
		allowsOther := stat.Mode&unix.S_IROTH != 0 && stat.Mode&unix.S_IWOTH != 0

		switch {
		case isOwner && !allowsOwner:
			return unix.EACCES
		case isGroupMember && !allowsGroup:
			return unix.EACCES
		case isOther && !allowsOther:
			return unix.EACCES
		}

		// walk up the directory tree
		dir := path.Dir(sockPath)
		for dir != "/" {
			err = unix.Stat(dir, &stat)
			if err != nil {
				return err
			}

			isOwner = stat.Uid == cred.Uid
			isGroupMember = (stat.Gid == cred.Gid) && !isOwner
			isOther = !isOwner && !isGroupMember

			// require execute permission
			allowsOwner := stat.Mode&unix.S_IXUSR != 0
			allowsGroup := stat.Mode&unix.S_IXGRP != 0
			allowsOther := stat.Mode&unix.S_IXOTH != 0

			switch {
			case isOwner && !allowsOwner:
				return unix.EACCES
			case isGroupMember && !allowsGroup:
				return unix.EACCES
			case isOther && !allowsOther:
				return unix.EACCES
			}

			dir = path.Dir(dir)
		}
	}

	// connect to the real ssh agent
	realConn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer realConn.Close()

	// proxy data
	tcpfwd.Pump2(conn, realConn.(*net.UnixConn))
	return nil
}
