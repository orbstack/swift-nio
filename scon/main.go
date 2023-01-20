package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"sync"
	"time"

	_ "net/http/pprof"

	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	seccompProxySock        = "/tmp/scon-seccomp.sock"
	gracefulShutdownTimeout = 100 * time.Millisecond
)

type ConManager struct {
	containers        map[string]*Container
	dataDir           string
	seccompPolicyPath string

	// refcounts
	forwards   map[HostForwardSpec]int
	forwardsMu sync.Mutex
}

func NewConManager(dataDir string) (*ConManager, error) {
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
	}, nil
}

func (m *ConManager) Close() error {
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

	// fmt.Println("create")
	// err = c.Create(options)
	// check(err)
}

func (m *ConManager) Get(name string) (*Container, bool) {
	c, bool := m.containers[name]
	return c, bool
}

func (m *ConManager) removeContainer(c *Container) error {
	delete(m.containers, c.name)
	runtime.SetFinalizer(c, nil)
	c.c.Release()
	return nil
}

type Container struct {
	name        string
	c           *lxc.Container
	defaultUser string

	agentProcess *os.Process
	manager      *ConManager
	mu           sync.RWMutex
}

func (c *Container) Stop() error {
	fmt.Println("lock..")
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Println("running?")
	if !c.Running() {
		fmt.Println("not running")
		return nil
	}

	fmt.Println("kil agent")
	if c.agentProcess != nil {
		err := c.agentProcess.Kill()
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		c.agentProcess = nil
	}

	// ignore failure
	fmt.Println("graceful shutdown")
	err := c.c.Shutdown(gracefulShutdownTimeout)
	if err != nil {
		logrus.Warn("graceful shutdown failed: ", err)
	} else {
		return nil
	}

	fmt.Println("force stop")
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

func main() {
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

	mgr, err := NewConManager(cwd + "/data")
	check(err)
	defer mgr.Close()

	// setup seccomp
	go runSeccompServer()
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
