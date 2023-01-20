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

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	seccompProxySock        = "/tmp/scon-seccomp.sock"
	gracefulShutdownTimeout = 100 * time.Millisecond
	startTimeout            = 10 * time.Second
)

type ConManager struct {
	containers        map[string]*Container
	dataDir           string
	seccompPolicyPath string
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

func (m *ConManager) setLxcConfigs(c *lxc.Container, name, logPath string) (err error) {
	defer func() {
		if err := recover(); err != nil {
			err = fmt.Errorf("failed to set LXC config: %w", err)
		}
	}()

	set := func(key, value string) {
		if err := c.SetConfigItem(key, value); err != nil {
			panic(err)
		}
	}

	/*
	 * from LXD
	 */
	set("lxc.pty.max", "1024")
	set("lxc.tty.max", "0")
	//set("lxc.cap.drop", "sys_time sys_module sys_rawio mac_admin mac_override")
	set("lxc.cap.drop", "sys_time")
	set("lxc.autodev", "1") // populate /dev

	// console
	//set("lxc.console.logfile", logPath + ".console.log")
	//set("lxc.console.buffer.size", "auto")
	//set("lxc.console.size", "auto")

	set("lxc.cgroup2.devices.deny", "a")
	set("lxc.cgroup2.devices.allow", "b *:* m")      // mknod block
	set("lxc.cgroup2.devices.allow", "c *:* m")      // mknod char
	set("lxc.cgroup2.devices.allow", "c 136:* rwm")  // dev/pts/*
	set("lxc.cgroup2.devices.allow", "c 1:3 rwm")    // dev/null
	set("lxc.cgroup2.devices.allow", "c 1:5 rwm")    // dev/zero
	set("lxc.cgroup2.devices.allow", "c 1:7 rwm")    // dev/full
	set("lxc.cgroup2.devices.allow", "c 1:8 rwm")    // dev/random
	set("lxc.cgroup2.devices.allow", "c 1:9 rwm")    // dev/urandom
	set("lxc.cgroup2.devices.allow", "c 5:0 rwm")    // dev/tty
	set("lxc.cgroup2.devices.allow", "c 5:1 rwm")    // dev/console
	set("lxc.cgroup2.devices.allow", "c 5:2 rwm")    // dev/ptmx
	set("lxc.cgroup2.devices.allow", "c 10:229 rwm") // dev/fuse
	set("lxc.cgroup2.devices.allow", "c 10:200 rwm") // dev/net/tun

	// Default mounts
	set("lxc.mount.auto", "proc:mixed sys:mixed cgroup:rw:force")
	set("lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional 0 0")
	set("lxc.mount.entry", "/dev/fuse dev/fuse none bind,create=file,optional 0 0")
	set("lxc.mount.entry", "/dev/net/tun dev/net/tun none bind,create=file,optional 0 0")
	set("lxc.mount.entry", "/proc/sys/fs/binfmt_misc proc/sys/fs/binfmt_misc none rbind,create=dir,optional 0 0")
	set("lxc.mount.entry", "/sys/fs/fuse/connections sys/fs/fuse/connections none rbind,create=dir,optional 0 0")
	set("lxc.mount.entry", "/sys/kernel/security sys/kernel/security none rbind,create=dir,optional 0 0")

	// nesting
	set("lxc.mount.entry", "proc dev/.lxc/proc proc create=dir,optional 0 0")
	set("lxc.mount.entry", "sys dev/.lxc/sys sysfs create=dir,optional 0 0")

	// other
	set("lxc.apparmor.profile", "unconfined")
	set("lxc.arch", "linux64")

	/*
	 * custom
	 */
	// seccomp
	set("lxc.seccomp.allow_nesting", "1")
	set("lxc.seccomp.notify.proxy", "unix:"+seccompProxySock)
	set("lxc.seccomp.profile", m.seccompPolicyPath)

	// network
	set("lxc.net.0.type", "veth")
	// TODO try router
	set("lxc.net.0.veth.mode", "bridge")
	set("lxc.net.0.link", ifBridge)

	// log
	set("lxc.log.file", logPath)
	if conf.Debug() {
		set("lxc.log.level", "trace")
	} else {
		set("lxc.log.level", "warn")
	}

	// container
	set("lxc.rootfs.path", "dir:"+path.Join(m.subdir("containers"), name, "rootfs"))
	set("lxc.uts.name", name)

	return nil
}

func (m *ConManager) newLxcContainer(name string) (*lxc.Container, error) {
	c, err := lxc.NewContainer(name, m.subdir("containers"))
	if err != nil {
		return nil, err
	}
	runtime.SetFinalizer(c, func(c *lxc.Container) {
		c.Release()
	})

	// logging
	logPath := path.Join(m.subdir("logs"), name+".log")
	c.ClearConfig()
	c.SetLogFile(logPath)
	if conf.Debug() {
		c.SetVerbosity(lxc.Verbose)
		c.SetLogLevel(lxc.TRACE)
	} else {
		c.SetVerbosity(lxc.Quiet)
		c.SetLogLevel(lxc.INFO)
	}

	// configs
	err = m.setLxcConfigs(c, name, logPath)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (m *ConManager) newContainer(name string) (*Container, error) {
	c, err := m.newLxcContainer(name)
	if err != nil {
		return nil, err
	}

	return &Container{
		name:        name,
		c:           c,
		defaultUser: "root", // TODO
		manager:     m,
	}, nil
}

func (m *ConManager) LoadExisting(name string) error {
	c, err := m.newContainer(name)
	if err != nil {
		return err
	}

	m.containers[name] = c
	return nil
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

func (c *Container) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.c.Start()
	if err != nil {
		return err
	}

	if !c.c.Wait(lxc.RUNNING, startTimeout) {
		return fmt.Errorf("container did not start: %s - %v", c.name, c.c.State())
	}

	err = c.startAgent()
	if err != nil {
		return err
	}

	return nil
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

func (c *Container) startAgent() error {
	// open /dev/null
	devNull, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	// fd is used directly, no pipe
	defer devNull.Close()

	cmd := &LxcCommand{
		CombinedArgs: []string{"/bin/su", "-l", "-c", "sleep inf"},
		Dir:          "/",
		Env:          []string{},
		Stdin:        devNull,
		Stdout:       devNull,
		Stderr:       devNull,
	}
	err = cmd.Start(c)
	if err != nil {
		return err
	}

	c.agentProcess = cmd.Process
	// Stop() hangs without this
	go cmd.Process.Wait()

	return nil
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
