package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "net/http/pprof"

	"github.com/lxc/go-lxc"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var (
	lxcConfigs = map[string]string{
		"lxc.seccomp.allow_nesting": "1",
		"lxc.seccomp.notify.proxy":  "unix:/tmp/seccomp.sock",
		"lxc.net":                   "",
		//"lxc.net.0.type":            "none",
	}
)

type ConManager struct {
	containers map[string]*Container
}

func (cm *ConManager) newLxcContainer(name string) (*lxc.Container, error) {
	c, err := lxc.NewContainer(name, "/tmp")
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (cm *ConManager) Create() {

}

func (cm *ConManager) Get() {

}

type Container struct {
	name        string
	c           *lxc.Container
	defaultUser string
}

func (c *Container) Start() error {
	return c.c.Start()
}

func (c *Container) Stop() error {
	return c.c.Stop()
}

func (c *Container) Delete() error {
	return c.c.Destroy()
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
	// get cwd
	cwd, err := os.Getwd()
	check(err)

	go runSconServer()
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
