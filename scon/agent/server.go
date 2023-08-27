package agent

import (
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/orbstack/macvirt/scon/agent/registry"
	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/agent/udpfwd"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/logutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	ProcessName = appid.AppName + "-helper"
)

type AgentServer struct {
	fdx          *Fdx
	tcpProxies   map[ProxySpec]*tcpfwd.TCPProxy
	udpProxies   map[ProxySpec]*udpfwd.UDPProxy
	loginManager *LoginManager

	localTCPRegistry *registry.LocalTCPRegistry

	docker *DockerAgent
}

type ProxySpec struct {
	IsIPv6 bool
	Port   uint16
}

type StartProxyArgs struct {
	ProxySpec
	FdxSeq uint64
}

type None struct{}

func (a *AgentServer) Ping(_ None, _ *None) error {
	return nil
}

// TODO fix zeroing: https://source.chromium.org/chromium/chromium/src/+/main:content/common/set_process_title_linux.cc
func setProcessCmdline(name string) error {
	argv0 := unsafe.Slice(unsafe.StringData(os.Args[0]), len(os.Args[0]))
	n := copy(argv0, name)
	if n < len(argv0) {
		// zero out the rest
		for i := n; i < len(argv0); i++ {
			argv0[i] = 0
		}
	}

	// zero out the rest of the args
	for i := 1; i < len(os.Args); i++ {
		argv := unsafe.Slice(unsafe.StringData(os.Args[i]), len(os.Args[i]))
		for j := 0; j < len(argv); j++ {
			argv[j] = 0
		}
	}

	return nil
}

func runAgent(rpcFile *os.File, fdxFile *os.File) error {
	// double fork so we get reparented to pidns init, away from scon
	if len(os.Args) >= 2 && os.Args[1] == "fork" {
		args := []string{os.Args[0]}
		args = append(args, os.Args[2:]...)
		_, err := os.StartProcess(os.Args[0], args, &os.ProcAttr{
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
			Sys: &syscall.SysProcAttr{
				Setsid: true,
			},
		})
		if err != nil {
			return err
		}

		os.Exit(0)
	}

	// our only unused fd
	os.Stdin = os.Stderr
	os.Stdout = os.Stderr

	// close executable fd now that we're running
	parts := strings.Split(os.Args[0], "/")
	exeFd, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return err
	}
	unix.Close(exeFd)

	// read args before we zero it out
	isDocker := slices.Contains(os.Args, "-docker")
	isK8s := slices.Contains(os.Args, "-k8s")

	// set process name
	err = setProcessCmdline(ProcessName)
	if err != nil {
		return err
	}

	rpcConn, err := net.FileConn(rpcFile)
	if err != nil {
		return err
	}
	// replace original fd (stdin) with stderr (console) in case anything writes to it
	unix.Dup2(int(os.Stderr.Fd()), int(rpcFile.Fd()))

	fdxConn, err := net.FileConn(fdxFile)
	if err != nil {
		return err
	}
	// replace original fd (stdout) with stderr (console) in case anything writes to it
	unix.Dup2(int(os.Stderr.Fd()), int(fdxFile.Fd()))

	// just in case
	runtime.KeepAlive(rpcFile)
	runtime.KeepAlive(fdxFile)

	// make docker client if we're the docker container
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	if isDocker {
		// remove vanity name
		hostname = "docker"
	}

	// now safe to init logrus
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
	}
	logrus.SetFormatter(logutil.NewPrefixFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
	}, "🌸 agent:"+hostname+" | "))

	fdx := NewFdx(fdxConn)
	server := &AgentServer{
		fdx:              fdx,
		tcpProxies:       make(map[ProxySpec]*tcpfwd.TCPProxy),
		udpProxies:       make(map[ProxySpec]*udpfwd.UDPProxy),
		localTCPRegistry: registry.NewLocalTCPRegistry(),
		loginManager:     NewLoginManager(),
	}
	rpcServer := rpc.NewServer()
	err = rpcServer.RegisterName("a", server)
	if err != nil {
		return err
	}

	if isDocker {
		server.docker, err = NewDockerAgent(isK8s)
		if err != nil {
			return err
		}
	}

	// Go sets soft rlimit = hard. bring it back down to avoid perf issues with fd closing in bad processes
	err = unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{
		Cur: 16384,
		Max: 1048576,
	})
	if err != nil {
		return err
	}

	// catch and ignore signals, so children exit first and rpc wait works better
	sigCh := make(chan os.Signal, 1)
	// TODO: catch SIGTERM and kill child processes so scon ssh can call wait() and read exit codes
	signal.Notify(sigCh, unix.SIGINT, unix.SIGQUIT, stopWarningSignal)
	go func() {
		for signal := range sigCh {
			switch signal {
			case stopWarningSignal:
				// warn docker about stop
				if server.docker != nil {
					err := server.docker.OnStop()
					if err != nil {
						logrus.WithError(err).Error("docker on-stop failed")
					}
				}
			}
		}
	}()

	// in NixOS, we need to wait for systemd before we do anything else (including running /bin/sh)
	waitForNixBoot()

	// now, run the system shell to get the PATH
	// we need this for running shell (su) and setup commands
	out, err := exec.Command("/bin/sh", "-lc", `echo "$PATH"`).CombinedOutput()
	if err != nil {
		logrus.WithError(err).WithField("output", string(out)).Error("failed to get PATH")
	} else {
		loginPath := strings.TrimSpace(string(out))
		logrus.WithField("path", loginPath).Debug("got PATH")
		os.Setenv("PATH", loginPath)
	}

	// start server!
	// fdx is used on-demand
	go rpcServer.ServeConn(rpcConn)

	if server.docker != nil {
		err := server.docker.PostStart()
		if err != nil {
			logrus.WithError(err).Error("docker post-start failed")
			// well, docker won't work...
			// just kill everything. nothing else should really be running
			// can't kill pid 1 with SIGKILL because unlike other procs, it has no handler by default
			_ = unix.Kill(1, unix.SIGTERM)
		}
	}

	runtime.Goexit()
	return nil
}

func Main() {
	err := runAgent(os.Stdin, os.Stdout)
	if err != nil {
		panic(err)
	}
}
