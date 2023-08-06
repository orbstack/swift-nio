package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/sysns"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// this entire thing is a hacky workaround for a VS Code bug
// https://github.com/microsoft/vscode/issues/168202
func RunSshAgentProxy(uid int, gid int) error {
	listener, err := listenUnixWithPerms(mounts.SshAgentProxySocket, 0600, uid, gid)
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

func dialAsUidGid(uid, gid uint32, network, address string) (net.Conn, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	oldUid, err := unix.SetfsuidRetUid(int(uid))
	if err != nil {
		return nil, err
	}
	oldGid, err := unix.SetfsgidRetGid(int(uid))
	if err != nil {
		return nil, err
	}
	defer unix.Setfsuid(oldUid)
	defer unix.Setfsgid(oldGid)

	return net.Dial(network, address)
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

	// past this point, we switch to the pid's mount ns to do stat, readlink, dial
	pidFd, err := unix.PidfdOpen(int(cred.Pid), 0)
	if err != nil {
		return fmt.Errorf("pidfd open: %w", err)
	}
	defer unix.Close(pidFd)

	return sysns.WithMountNs1(pidFd, func() error {
		// should we proxy this?
		if strings.HasPrefix(path.Base(sockPath), "vscode-ssh-auth-sock-") {
			// fix it IFF it doesn't exist
			if _, err := os.Lstat(sockPath); errors.Is(err, os.ErrNotExist) {
				sockPath = mounts.SshAgentSocket
			}
		}

		// resolve socket path relative to process cwd
		if !path.IsAbs(sockPath) {
			cwd, err := os.Readlink(procPid + "/cwd")
			if err != nil {
				return err
			}

			sockPath = path.Join(cwd, sockPath)
		}

		// connect to the real ssh agent (w/ uid and gid, race-free)
		realConn, err := dialAsUidGid(cred.Uid, cred.Gid, "unix", sockPath)
		if err != nil {
			return err
		}
		defer realConn.Close()

		// proxy data
		tcpfwd.Pump2(conn, realConn.(*net.UnixConn))
		return nil
	})
}
