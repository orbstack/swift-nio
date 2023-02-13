package agent

import (
	"context"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
	"github.com/kdrag0n/macvirt/scon/agent/udpfwd"
	"github.com/kdrag0n/macvirt/scon/conf"
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
	dockerClient *http.Client
}

type ProxySpec struct {
	IsIPv6 bool
	Port   uint16
}

type StartProxyArgs struct {
	ProxySpec
	FdxSeq uint64
}

// Never obfuscate the AgentServer type (garble)
var _ = reflect.TypeOf(AgentServer{})

type None struct{}

func (a *AgentServer) Ping(_ None, _ *None) error {
	return nil
}

func (a *AgentServer) GetListeners(_ None, reply *[]ProcListener) error {
	listeners, err := readAllProcNet()
	if err != nil {
		return err
	}

	*reply = listeners
	return nil
}

func (a *AgentServer) OpenDiagNetlink(_ None, reply *uint64) error {
	// open netlink socket
	// cloexec safe
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_INET_DIAG)
	if err != nil {
		return err
	}

	// set nonblock (persists over SCM_RIGHTS transfer)
	err = unix.SetNonblock(fd, true)
	if err != nil {
		return err
	}

	// send over fdx
	seq, err := a.fdx.SendFdInt(fd)
	if err != nil {
		return err
	}

	// close original fd
	unix.Close(fd)

	*reply = seq
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

	proxy := tcpfwd.NewTCPProxy(listener, spec.IsIPv6, spec.Port)
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
	signal.Notify(sigCh, unix.SIGINT, unix.SIGTERM, unix.SIGQUIT)
	go func() {
		for range sigCh {
		}
	}()

	rpcConn, err := net.FileConn(rpcFile)
	if err != nil {
		return err
	}
	// replace original fd with stderr
	unix.Dup2(int(os.Stderr.Fd()), int(rpcFile.Fd()))

	fdxConn, err := net.FileConn(fdxFile)
	if err != nil {
		return err
	}
	// replace original fd with stderr
	unix.Dup2(int(os.Stderr.Fd()), int(fdxFile.Fd()))

	// now safe to init logrus
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "01-02 15:04:05",
		})
	}

	// make docker client if we're the docker container
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	var dockerClient *http.Client
	if hostname == "docker" {
		// use default unix socket
		dockerClient, err = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", "/var/run/docker.sock")
				},
				MaxIdleConns: 2,
			},
		}, nil
	}

	fdx := NewFdx(fdxConn)
	server := &AgentServer{
		fdx:          fdx,
		tcpProxies:   make(map[ProxySpec]*tcpfwd.TCPProxy),
		udpProxies:   make(map[ProxySpec]*udpfwd.UDPProxy),
		dockerClient: dockerClient,
	}
	rpcServer := rpc.NewServer()
	err = rpcServer.RegisterName("a", server)
	if err != nil {
		return err
	}

	// fdx is used on-demand
	go rpcServer.ServeConn(rpcConn)

	runtime.Goexit()
	return nil
}

func Main() {
	err := runAgent(os.Stdin, os.Stdout)
	if err != nil {
		panic(err)
	}
}
