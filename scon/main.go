package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	_ "net/http/pprof"

	"github.com/coreos/go-iptables/iptables"
	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/lxc/go-lxc"
	seccomp "github.com/seccomp/libseccomp-golang"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	// TODO last used
	defaultContainer = "alpine"
	defaultUser      = "root"

	ifBridge = "sconbr0"
)

var (
	lxcConfigs = map[string]string{
		"lxc.seccomp.allow_nesting": "1",
		"lxc.seccomp.notify.proxy":  "unix:/tmp/seccomp.sock",
		"lxc.net":                   "",
		//"lxc.net.0.type":            "none",
	}
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

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
		if ssh.AgentRequested(s) {
			// TODO path in container
			l, err := ssh.NewAgentListener()
			check(err)
			defer l.Close()
			go ssh.ForwardAgentConnections(l, s)
			env = append(env, "SSH_AUTH_SOCK="+l.Addr().String())
		}

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

func newBridge() (*netlink.Bridge, error) {
	la := netlink.NewLinkAttrs()
	la.Name = ifBridge
	la.MTU = 1500
	la.TxQLen = 10000
	bridge := &netlink.Bridge{LinkAttrs: la}
	err := netlink.LinkAdd(bridge)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = netlink.LinkDel(bridge)
			if err != nil {
				return nil, err
			}
		}
		return nil, err
	}
	// add ip
	addr, err := netlink.ParseAddr("172.30.31.1/24")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}
	// add ipv6
	addr, err = netlink.ParseAddr("fc00:30:31::1/64")
	if err != nil {
		return nil, err
	}
	err = netlink.AddrAdd(bridge, addr)
	if err != nil {
		return nil, err
	}
	// set up
	err = netlink.LinkSetUp(bridge)
	if err != nil {
		return nil, err
	}
	return bridge, nil
}

func newVethPair(bridge *netlink.Bridge) (*netlink.Veth, error) {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name:   "veth0a",
			MTU:    bridge.MTU,
			TxQLen: bridge.TxQLen,
		},
		PeerName: "veth0b",
	}
	err := netlink.LinkAdd(veth)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			err = netlink.LinkDel(veth)
			if err != nil {
				return nil, err
			}
		}
		return nil, err
	}
	err = netlink.LinkSetMaster(veth, bridge)
	if err != nil {
		return nil, err
	}
	// set up
	err = netlink.LinkSetUp(veth)
	if err != nil {
		return nil, err
	}
	return veth, nil
}

func setupNat() (func() error, error) {
	ipt, err := iptables.New(iptables.IPFamily(iptables.ProtocolIPv4), iptables.Timeout(5))
	if err != nil {
		return nil, err
	}

	// TODO interface?
	err = ipt.AppendUnique("nat", "POSTROUTING", "-s", "172.30.31.0/24", "!", "-d", "172.30.31.0/24", "-j", "MASQUERADE")
	if err != nil {
		return nil, err
	}

	err = ipt.AppendUnique("filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", "172.30.30.200", "-j", "REJECT", "--reject-with", "tcp-reset")
	if err != nil {
		return nil, err
	}

	return func() error {
		err = ipt.DeleteIfExists("nat", "POSTROUTING", "-s", "172.30.31.0/24", "!", "-d", "172.30.31.0/24", "-j", "MASQUERADE")
		if err != nil {
			return err
		}

		err = ipt.DeleteIfExists("filter", "FORWARD", "-i", ifBridge, "--proto", "tcp", "-d", "172.30.30.200", "-j", "REJECT", "--reject-with", "tcp-reset")
		if err != nil {
			return err
		}

		return nil
	}, nil
}

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
		fmt.Println("req", req)

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

func runControlServer() error {
	mux := http.NewServeMux()
	return http.ListenAndServe(":8080", mux)
}

func runPprof() {
	log.Println(http.ListenAndServe("localhost:6060", nil))
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

		conn.Close()
	}
}

func main() {
	// get cwd
	cwd, err := os.Getwd()
	check(err)

	go runControlServer()
	go runPprof()
	go runSeccompServer()

	storageDir := cwd + "/data"
	logPath := cwd + "/data/alpine.log"
	c, err := lxc.NewContainer("alpine", storageDir)
	check(err)
	defer c.Release()

	fmt.Println(c.Name())
	c.SetVerbosity(lxc.Verbose)
	c.SetLogFile(logPath)
	c.SetLogLevel(lxc.TRACE)

	// options := lxc.TemplateOptions{
	// 	Template: "download",
	// 	Backend:  lxc.Directory,
	// 	Distro:   "alpine",
	// 	Release:  "edge",
	// 	Arch:     "amd64", // TODO
	// 	Variant:  "default",
	// 	//FlushCache: true,
	// }

	// fmt.Println("create")
	// err = c.Create(options)
	// check(err)

	err = c.SetConfigItem("lxc.seccomp.profile", cwd+"/policy.seccomp")
	check(err)
	for k, v := range lxcConfigs {
		err = c.SetConfigItem(k, v)
		check(err)
	}

	fmt.Println("start")
	err = c.Start()
	check(err)

	// seccompFd, err := c.SeccompNotifyFdActive()
	// check(err)
	// go monitorSeccompNotifier(c, seccomp.ScmpFd(seccompFd.Fd()))
	// defer seccompFd.Close()

	fmt.Println("wait running")
	if c.Wait(lxc.RUNNING, 5*time.Second) {
		fmt.Println("running")
	} else {
		fmt.Println("not running")
	}

	bridge, err := newBridge()
	check(err)
	defer netlink.LinkDel(bridge)

	veth, err := newVethPair(bridge)
	check(err)
	defer netlink.LinkDel(veth)

	cleanupNat, err := setupNat()
	check(err)
	defer cleanupNat()

	// TODO attach at boot
	err = c.AttachInterface("veth0b", "eth0")
	check(err)

	fmt.Println("run agent")
	svcPid, err := c.RunCommandNoWait([]string{"/bin/su", "-l", "-c", "sleep inf"}, lxc.DefaultAttachOptions)
	fmt.Println("agent pid", svcPid, err)
	check(err)

	// fmt.Println("wait net")
	// ips, err := c.WaitIPAddresses(5 * time.Second)
	// if err == nil {
	// 	fmt.Println("net", ips)
	// } else {
	// 	fmt.Println("no net")
	// }

	containerMap := map[string]*lxc.Container{
		"alpine": c,
	}
	go runSSHServer(containerMap)

	fmt.Println("shell")
	shellPid, err := c.RunCommandNoWait([]string{"/bin/su", "-l"}, lxc.DefaultAttachOptions)
	fmt.Println("shell status", shellPid, err)
	check(err)

	unix.Wait4(shellPid, nil, 0, nil)

	fmt.Println("shutdown")
	err = c.Shutdown(1 * time.Second)
	check(err)

	fmt.Println("stop")
	err = c.Stop()
	check(err)
}
