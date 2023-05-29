package agent

import (
	"context"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/macvmgr/dockertypes"
	"github.com/orbstack/macvirt/macvmgr/logutil"
	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/agent/udpfwd"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	ProcessName = appid.AppName + "-helper"

	// mitigate risk of 512k tcp buffers using too much memory: not too aggressive oom adj
	oomScoreAdjCriticalGuest = "-500"
)

type AgentServer struct {
	fdx           *Fdx
	tcpProxies    map[ProxySpec]*tcpfwd.TCPProxy
	udpProxies    map[ProxySpec]*udpfwd.UDPProxy
	loginManager  *LoginManager
	containerName string

	dockerClient         *http.Client
	dockerRunning        syncx.CondBool
	dockerMu             syncx.Mutex
	dockerHost           *hclient.Client
	dockerContainerBinds map[string][]string
	dockerLastContainers []dockertypes.ContainerSummaryMin
	// refreshing w/ debounce+diff ensures consistent snapshots
	dockerRefreshDebounce   syncx.FuncDebounce
	dockerUIEventDebounce   syncx.FuncDebounce
	dockerPendingUIEntities []dockertypes.UIEntity
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

func (a *AgentServer) StartProxyTCP(args StartProxyArgs, _ *None) error {
	spec := args.ProxySpec
	listenerFd, err := a.fdx.RecvFile(args.FdxSeq)
	if err != nil {
		return err
	}

	listener, err := net.FileListener(listenerFd)
	if err != nil {
		return err
	}
	listenerFd.Close()

	// Docker: always prefer v4 because Docker is traditionally v4-only
	// still try v6 in case of host net and v6-only servers
	preferV6 := spec.IsIPv6 && a.containerName != "docker"
	proxy := tcpfwd.NewTCPProxy(listener, preferV6, spec.Port)
	a.tcpProxies[spec] = proxy
	go proxy.Run()

	return nil
}

func (a *AgentServer) StartProxyUDP(args StartProxyArgs, _ *None) error {
	spec := args.ProxySpec
	listenerFd, err := a.fdx.RecvFile(args.FdxSeq)
	if err != nil {
		return err
	}

	udpConn, err := net.FilePacketConn(listenerFd)
	if err != nil {
		return err
	}

	proxy, err := udpfwd.NewUDPLocalProxy(udpConn, spec.IsIPv6, spec.Port)
	if err != nil {
		return err
	}
	a.udpProxies[spec] = proxy
	go proxy.Run()

	return nil
}

func (a *AgentServer) StopProxyTCP(args ProxySpec, _ *None) error {
	proxy, ok := a.tcpProxies[args]
	if !ok {
		return nil
	}

	proxy.Close()
	delete(a.tcpProxies, args)

	return nil
}

func (a *AgentServer) StopProxyUDP(args ProxySpec, _ *None) error {
	proxy, ok := a.udpProxies[args]
	if !ok {
		return nil
	}

	proxy.Close()
	delete(a.udpProxies, args)

	return nil
}

// TODO fix zeroing: https://source.chromium.org/chromium/chromium/src/+/main:content/common/set_process_title_linux.cc
func setProcessCmdline(name string) error {
	argv0str := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[0]))
	argv0 := (*[1 << 30]byte)(unsafe.Pointer(argv0str.Data))[:argv0str.Len]

	n := copy(argv0, name)
	if n < len(argv0) {
		// zero out the rest
		for i := n; i < len(argv0); i++ {
			argv0[i] = 0
		}
	}

	return nil
}

func runAgent(rpcFile *os.File, fdxFile *os.File) error {
	// double fork so we get reparented to pidns init, away from scon
	if len(os.Args) == 2 {
		_, err := os.StartProcess(os.Args[0], []string{os.Args[0]}, &os.ProcAttr{
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

	// set process name
	err = setProcessCmdline(ProcessName)
	if err != nil {
		return err
	}

	// catch and ignore signals, so children exit first
	// so rpc wait works better
	sigCh := make(chan os.Signal, 1)
	// TODO: catch SIGTERM and kill child processes so scon ssh can call wait() and read exit codes
	signal.Notify(sigCh, unix.SIGINT, unix.SIGQUIT)
	go func() {
		for range sigCh {
		}
	}()

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
		fdx:           fdx,
		tcpProxies:    make(map[ProxySpec]*tcpfwd.TCPProxy),
		udpProxies:    make(map[ProxySpec]*udpfwd.UDPProxy),
		loginManager:  NewLoginManager(),
		containerName: hostname,
	}
	rpcServer := rpc.NewServer()
	err = rpcServer.RegisterName("a", server)
	if err != nil {
		return err
	}

	if hostname == "docker" {
		// use default unix socket
		server.dockerClient = &http.Client{
			// no timeout - we do event monitoring
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", "/var/run/docker.sock")
				},
				// idle conns are ok here because we get frozen along with docker
				MaxIdleConns: 2,
			},
		}

		server.dockerRunning = syncx.NewCondBool()
		server.dockerContainerBinds = make(map[string][]string)
		server.dockerRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
			err := server.dockerRefreshContainers()
			if err != nil {
				logrus.WithError(err).Error("failed to refresh docker containers")
			}
		})
		server.dockerUIEventDebounce = syncx.NewFuncDebounce(dockerUIEventDebounce, func() {
			err := server.dockerDoSendUIEvent()
			if err != nil {
				logrus.WithError(err).Error("failed to send docker UI event")
			}
		})
	}

	// Go sets soft rlimit = hard. bring it back down to avoid perf issues
	err = unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{
		Cur: 16384,
		Max: 1048576,
	})
	if err != nil {
		return err
	}

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

	if hostname == "docker" {
		go func() {
			err := server.dockerPostStart()
			if err != nil {
				logrus.WithError(err).Error("docker post-start failed")
			}
		}()

		// docker-init oom score adj
		// dockerd's score is set via cmdline argument
		err = os.WriteFile("/proc/1/oom_score_adj", []byte(oomScoreAdjCriticalGuest), 0644)
		if err != nil {
			return err
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
