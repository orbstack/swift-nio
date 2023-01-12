package sshsrv

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	// we don't use ssh for security, so hard-code for fast startup
	hostKeyEd25519 = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACAgEJD3oK7ddXQktDsupy91mk85nbFM12Y6srQ0ujq4oAAAAKDLA5G2ywOR
tgAAAAtzc2gtZWQyNTUxOQAAACAgEJD3oK7ddXQktDsupy91mk85nbFM12Y6srQ0ujq4oA
AAAEAdZQRbxMDW6DaGP2YY8yxby24cwECktHygG1dGxHmuFiAQkPegrt11dCS0Oy6nL3Wa
TzmdsUzXZjqytDS6OrigAAAAFmRyYWdvbkBhbmRyb21lZGEubG9jYWwBAgMEBQYH
-----END OPENSSH PRIVATE KEY-----`
)

func handleSshConn(s ssh.Session) error {
	ptyReq, winCh, isPty := s.Pty()

	logrus.WithFields(logrus.Fields{
		"pty":  isPty,
		"user": s.User(),
		"cmd":  s.RawCommand(),
	}).Info("SSH connection")

	// extract __MV_PWD from env
	env := s.Environ()
	newEnv := make([]string, 0, len(env))
	var pwd string
	for _, kv := range s.Environ() {
		if strings.HasPrefix(kv, "__MV_PWD=") {
			pwd = kv[10:]
		} else {
			newEnv = append(newEnv, kv)
		}
	}
	var err error
	if pwd == "" {
		pwd, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	newEnv = append(newEnv, "TERM="+ptyReq.Term)

	// TODO no shell for speed
	var cmdArgs []string
	if s.RawCommand() != "" {
		cmdArgs = append(cmdArgs, "-c", s.RawCommand())
	}
	shell := os.Getenv("SHELL")
	cmd := exec.Command(shell, cmdArgs...)
	cmd.Env = newEnv
	cmd.Dir = pwd

	if isPty {
		ptyF, err := pty.StartWithSize(cmd, &pty.Winsize{
			Rows: uint16(ptyReq.Window.Height),
			Cols: uint16(ptyReq.Window.Width),
		})
		if err != nil {
			return err
		}
		defer ptyF.Close()

		go func() {
			for win := range winCh {
				pty.Setsize(ptyF, &pty.Winsize{
					Rows: uint16(win.Height),
					Cols: uint16(win.Width),
				})
			}
		}()

		go io.Copy(ptyF, s)
		go io.Copy(s, ptyF)
	} else {
		cmd.Stdin = s
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()

		err := cmd.Start()
		if err != nil {
			return err
		}
	}

	// don't wait for fds to close, we close them
	// read-side pipes will be closed after start
	// write-side pipes will be closed on EOF
	ps, err := cmd.Process.Wait()
	if err != nil {
		return err
	}
	if !ps.Success() {
		return &exec.ExitError{ProcessState: ps}
	}

	return nil
}

func ListenHostSSH(stack *stack.Stack, address tcpip.Address) error {
	handler := func(s ssh.Session) {
		defer s.Close()

		err := handleSshConn(s)
		if err != nil {
			if exitErr := err.(*exec.ExitError); exitErr != nil {
				// all ok, just exit
				s.Exit(exitErr.ExitCode())
			} else {
				logrus.Error("SSH error: ", err)
				s.Write([]byte("SSH error: " + err.Error() + "\r\n"))
				s.Exit(1)
			}
		}

		s.Exit(0)
	}

	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.ServiceHostSSH,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	go func() {
		err = ssh.Serve(listener, handler, ssh.HostKeyPEM([]byte(hostKeyEd25519)))
		if err != nil {
			logrus.Error("hostssh: Serve() =", err)
		}
	}()

	return nil
}
