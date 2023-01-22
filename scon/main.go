package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	seccompProxySock               = "/tmp/scon-seccomp.sock"
	gracefulShutdownTimeoutRelease = 3 * time.Second
	gracefulShutdownTimeoutDebug   = 100 * time.Millisecond

	cmdContainerManager = "container-manager"
	cmdAgent            = "agent"
)

type ConManager struct {
	containers        map[string]*Container
	dataDir           string
	seccompPolicyPath string
	seccompCookies    map[uint64]*Container

	host       *hclient.Client
	forwards   map[agent.ProcListener]ForwardState
	forwardsMu sync.Mutex
}

func NewConManager(dataDir string, hc *hclient.Client) (*ConManager, error) {
	// extract seccomp policy
	f, err := os.CreateTemp("", "scon-seccomp.policy")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = f.WriteString(seccompPolicy)
	if err != nil {
		return nil, err
	}

	return &ConManager{
		containers:        make(map[string]*Container),
		dataDir:           dataDir,
		seccompPolicyPath: f.Name(),
		seccompCookies:    make(map[uint64]*Container),

		host:     hc,
		forwards: make(map[agent.ProcListener]ForwardState),
	}, nil
}

func (m *ConManager) Close() error {
	m.host.Close()
	os.Remove(m.seccompPolicyPath)
	return nil
}

func (m *ConManager) subdir(dir string) string {
	path := path.Join(m.dataDir, dir)
	if err := os.MkdirAll(path, 0755); err != nil {
		panic(err)
	}
	return path
}

func (m *ConManager) Create() {
	// TODO

	// options := lxc.TemplateOptions{
	// 	Template: "download",
	// 	Backend:  lxc.Directory,
	// 	Distro:   "alpine",
	// 	Release:  "edge",
	// 	Arch:     "amd64", // TODO
	// 	Variant:  "default",
	// 	//FlushCache: true,
	// }

	// err = c.Create(options)
	// check(err)
}

func (m *ConManager) Get(name string) (*Container, bool) {
	c, bool := m.containers[name]
	return c, bool
}

func (m *ConManager) removeContainer(c *Container) error {
	delete(m.containers, c.name)
	delete(c.manager.seccompCookies, c.seccompCookie)
	runtime.SetFinalizer(c, nil)
	c.c.Release()
	return nil
}

type Container struct {
	name        string
	c           *lxc.Container
	defaultUser string

	agent   syncx.CondValue[*agent.Client]
	manager *ConManager
	mu      sync.RWMutex

	seccompCookie          uint64
	lastListeners          []agent.ProcListener
	listenerUpdateDebounce syncx.FuncDebounce
}

func (c *Container) Agent() *agent.Client {
	return c.agent.Wait()
}

func (c *Container) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.Running() {
		return nil
	}

	// stop forwards
	for _, listener := range c.lastListeners {
		c.manager.removeForward(c, listener)
	}

	// stop agent (after listeners removed)
	if c.agent.Get() != nil {
		c.Agent().Close()
		c.agent.Set(nil)
	}

	// ignore failure
	timeout := gracefulShutdownTimeoutRelease
	if conf.Debug() {
		timeout = gracefulShutdownTimeoutDebug
	}
	err := c.c.Shutdown(timeout)
	if err != nil {
		logrus.Warn("graceful shutdown failed: ", err)
	} else {
		return nil
	}

	err = c.c.Stop()
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) Delete() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.c.Running() {
		err := c.Stop()
		if err != nil {
			return err
		}
	}

	err := c.c.Destroy()
	if err != nil {
		return err
	}

	return c.manager.removeContainer(c)
}

func (c *Container) Exec(cmd []string, opts lxc.AttachOptions) (int, error) {
	return c.c.RunCommandNoWait(cmd, opts)
}

func (c *Container) Running() bool {
	return c.c.Running()
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func runPprof() {
	log.Println(http.ListenAndServe("localhost:6060", nil))
}

func runContainerManager() {
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "01-02 15:04:05",
		})
	}

	// data dir
	cwd, err := os.Getwd()
	check(err)

	// connect to hcontrol (ownership taken by cmgr)
	if conf.C().DummyHcontrol {
		err = hclient.StartDummyServer()
		check(err)
	}
	hcontrolConn, err := net.Dial("tcp", conf.C().HcontrolIP+":"+strconv.Itoa(ports.HostHcontrol))
	check(err)
	hc, err := hclient.New(hcontrolConn)
	check(err)

	mgr, err := NewConManager(cwd+"/data", hc)
	check(err)
	defer mgr.Close()

	// setup seccomp
	go mgr.serveSeccomp()
	// setup network
	bridge, err := newBridge()
	check(err)
	defer netlink.LinkDel(bridge)

	cleanupNat, err := setupNat()
	check(err)
	defer cleanupNat()

	// services
	go mgr.ListenSSH("127.0.0.1:2222")
	go runSconServer(mgr)
	if conf.Debug() {
		go runPprof()
	}

	err = mgr.LoadExisting("alpine")
	check(err)
	container, ok := mgr.Get("alpine")
	if !ok {
		panic("container not found")
	}

	fmt.Println("start")
	err = container.Start()
	check(err)

	fmt.Println("wait sig")
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, unix.SIGINT, unix.SIGTERM)
	<-sigChan

	fmt.Println("stop")
	err = container.Stop()
	check(err)

	fmt.Println("all done")
}

func main() {
	cmd := cmdContainerManager
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case cmdContainerManager:
		runContainerManager()
	case cmdAgent:
		agent.Main()
	default:
		panic("unknown command: " + cmd)
	}
}
