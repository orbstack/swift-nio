package agent

import (
	"net"
	"net/rpc"
	"os"
	"reflect"
	"runtime"

	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
	"github.com/kdrag0n/macvirt/scon/agent/udpfwd"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type AgentServer struct {
	fdx        *Fdx
	tcpProxies map[ProxySpec]*tcpfwd.TCPProxy
	udpProxies map[ProxySpec]*udpfwd.UDPProxy
}

type ProxySpec struct {
	IsIPv6 bool
	Port   uint16
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

func (a *AgentServer) OpenDiagNetlink(_ None, _ *None) error {
	// open netlink socket
	// cloexec safe
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_INET_DIAG)
	if err != nil {
		return err
	}

	// send over fdx
	err = a.fdx.SendFdInt(fd)
	if err != nil {
		return err
	}

	// close original fd
	unix.Close(fd)

	return nil
}

func (a *AgentServer) StartProxyTCP(spec ProxySpec, _ *None) error {
	listenerFd, err := a.fdx.RecvFile()
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

func (a *AgentServer) StartProxyUDP(spec ProxySpec, _ *None) error {
	listenerFd, err := a.fdx.RecvFile()
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

func runAgent(rpcFile *os.File, fdxFile *os.File) error {
	// our only unused fd
	os.Stdin = os.Stderr
	os.Stdout = os.Stderr

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

	fdx := NewFdx(fdxConn)
	server := &AgentServer{
		fdx:        fdx,
		tcpProxies: make(map[ProxySpec]*tcpfwd.TCPProxy),
		udpProxies: make(map[ProxySpec]*udpfwd.UDPProxy),
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
