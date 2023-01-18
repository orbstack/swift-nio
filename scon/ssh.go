package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"
)

const (
	// TODO last used
	defaultContainer = "alpine"
	defaultUser      = "root"
)

func runSSHServer(containers map[string]*lxc.Container) {
	ssh.Handle(func(s ssh.Session) {
		defer s.Close()

		fmt.Println("ssh session")
		ptyReq, winCh, isPty := s.Pty()

		fmt.Println("pty", ptyReq)
		userReq := s.User()
		userParts := strings.Split(userReq, "@")
		var user, containerName string
		if len(userParts) > 2 {
			io.WriteString(s, "Invalid user\n")
			s.Exit(1)
			return
		}
		if len(userParts) == 2 {
			user = userParts[0]
			containerName = userParts[1]
		} else {
			user = defaultUser
			containerName = userParts[0]
		}
		if containerName == "default" {
			containerName = defaultContainer
		}

		fmt.Println("user", user, "container", containerName)

		container, ok := containers[containerName]
		// try default container
		if !ok && len(userParts) == 1 {
			container, ok = containers[defaultContainer]
			if ok {
				containerName = defaultContainer
				user = userParts[0]
			}
		}
		if !ok {
			io.WriteString(s, "Container not found\n")
			s.Exit(1)
			return
		}

		fmt.Println("container", container.Name())
		if !container.Running() {
			fmt.Println("starting container")
			err := container.Start()
			check(err)
		}

		env := s.Environ()
		env = append(env, "TERM="+ptyReq.Term)

		var childPid int
		attachOptions := lxc.AttachOptions{
			Namespaces: -1,
			Arch:       -1,
			Cwd:        "/",
			UID:        0,
			GID:        0,
			Groups:     nil,
			ClearEnv:   true,
			Env:        env,
			EnvToKeep:  nil,
			// filled in below
			StdinFd:            0,
			StdoutFd:           0,
			StderrFd:           0,
			RemountSysProc:     false,
			ElevatedPrivileges: false,
		}
		fmt.Println("cmd", s.RawCommand())
		if isPty {
			// TODO open in container
			ptyF, tty, err := pty.Open()
			check(err)
			defer ptyF.Close()
			defer tty.Close()

			fmt.Println("ptyF", ptyF, "tty", tty)
			pty.Setsize(ptyF, &pty.Winsize{
				Rows: uint16(ptyReq.Window.Height),
				Cols: uint16(ptyReq.Window.Width),
			})

			attachOptions.StdinFd = tty.Fd()
			attachOptions.StdoutFd = tty.Fd()
			attachOptions.StderrFd = tty.Fd()
			// starts login session
			childPid, err = container.RunCommandNoWait([]string{"/bin/su", "-l", user}, attachOptions)
			check(err)

			fmt.Println("childPid", childPid)

			go func() {
				for win := range winCh {
					fmt.Println("win", win)
					pty.Setsize(ptyF, &pty.Winsize{
						Rows: uint16(win.Height),
						Cols: uint16(win.Width),
					})
				}
			}()

			go io.Copy(ptyF, s)
			go io.Copy(s, ptyF)
		} else {
			var stdinPipes [2]int
			var stdoutPipes [2]int
			var stderrPipes [2]int
			err := unix.Pipe2(stdinPipes[:], unix.O_CLOEXEC|unix.O_NONBLOCK)
			check(err)
			err = unix.Pipe2(stdoutPipes[:], unix.O_CLOEXEC|unix.O_NONBLOCK)
			check(err)
			err = unix.Pipe2(stderrPipes[:], unix.O_CLOEXEC|unix.O_NONBLOCK)
			check(err)

			attachOptions.StdinFd = uintptr(stdinPipes[0])
			attachOptions.StdoutFd = uintptr(stdoutPipes[1])
			attachOptions.StderrFd = uintptr(stderrPipes[1])
			childPid, err = container.RunCommandNoWait([]string{"/bin/su", "-l", user, "-c", s.RawCommand()}, attachOptions)
			check(err)

			stdinWriteFile := os.NewFile(uintptr(stdinPipes[1]), "stdin")
			stdoutReadFile := os.NewFile(uintptr(stdoutPipes[0]), "stdout")
			stderrReadFile := os.NewFile(uintptr(stderrPipes[0]), "stderr")
			defer stdinWriteFile.Close()
			defer stdoutReadFile.Close()
			defer stderrReadFile.Close()

			go io.Copy(stdinWriteFile, s)
			go io.Copy(s, stdoutReadFile)
			go io.Copy(s.Stderr(), stderrReadFile)

			fmt.Println("childPid", childPid)
		}

		fmt.Println("wait")
		var status unix.WaitStatus
		_, err := unix.Wait4(int(childPid), &status, 0, nil)
		check(err)
		fmt.Println("wait done", status.ExitStatus())
		err = s.Exit(status.ExitStatus())
		check(err)
	})

	log.Fatal(ssh.ListenAndServe(":2222", nil, ssh.HostKeyFile("host_keys/ssh_host_rsa_key")))
}
