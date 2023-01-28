package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	AppName = "scon"

	cmdContainerManager = "container-manager"
)

type ConManager struct {
	// config
	dataDir           string
	tmpDir            string
	lxcDir            string
	seccompPolicyPath string
	seccompProxySock  string

	// state
	containersByID   map[string]*Container
	containersByName map[string]*Container
	containersMu     sync.RWMutex
	seccompCookies   map[uint64]*Container
	stopping         bool

	// services
	db   *Database
	host *hclient.Client

	// auto forward
	forwards   map[agent.ProcListener]ForwardState
	forwardsMu sync.Mutex

	// stop
	stopChan chan struct{}

	// network
	net *Network
}

func NewConManager(dataDir string, hc *hclient.Client) (*ConManager, error) {
	// tmp dir
	tmpDir, err := os.MkdirTemp("", AppName)
	if err != nil {
		return nil, err
	}

	// extract seccomp policy
	seccompPolicyPath := path.Join(tmpDir, "seccomp.policy")
	seccompProxySock := path.Join(tmpDir, "seccomp.sock")
	err = os.WriteFile(seccompPolicyPath, []byte(seccompPolicy), 0644)
	if err != nil {
		return nil, err
	}

	lxcDir := path.Join(tmpDir, "lxc")
	err = os.Mkdir(lxcDir, 0755)
	if err != nil {
		return nil, err
	}

	// data
	err = os.MkdirAll(dataDir, 0755)
	if err != nil {
		return nil, err
	}

	db, err := OpenDatabase(path.Join(dataDir, "store.db"))
	if err != nil {
		return nil, err
	}

	mgr := &ConManager{
		dataDir:           dataDir,
		tmpDir:            tmpDir,
		lxcDir:            lxcDir,
		seccompPolicyPath: seccompPolicyPath,
		seccompProxySock:  seccompProxySock,

		containersByID:   make(map[string]*Container),
		containersByName: make(map[string]*Container),
		seccompCookies:   make(map[uint64]*Container),

		db:   db,
		host: hc,

		forwards: make(map[agent.ProcListener]ForwardState),

		stopChan: make(chan struct{}),
	}
	mgr.net = NewNetwork(mgr.subdir("network"))

	return mgr, nil
}

func (m *ConManager) Start() error {
	// network
	err := m.net.Start()
	if err != nil {
		return err
	}

	// essential services for starting containers
	go m.serveSeccomp()

	// restore and start!
	m.restoreContainers()

	// services
	go runSconServer(m)
	go m.ListenSSH(conf.C().SSHListen)

	// periodic tasks
	go m.runAutoForwardGC()

	logrus.Info("started")
	return err
}

func (m *ConManager) Close() error {
	m.stopping = true
	m.stopAll()

	logrus.Debug("finish cleanup")
	m.host.Close()
	m.net.Close()
	m.stopChan <- struct{}{}
	close(m.stopChan)
	os.RemoveAll(m.tmpDir) // seecomp and lxc
	return nil
}

func (m *ConManager) stopAll() {
	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	logrus.Info("stopping all containers")
	var wg sync.WaitGroup
	for _, c := range m.containersByID {
		wg.Add(1)
		go func(c *Container) {
			defer wg.Done()

			err := c.Stop()
			if err != nil {
				logrus.WithError(err).Error("failed to stop container for manager shutdown")
			}
		}(c)
	}
	wg.Wait()
}

func (m *ConManager) subdir(dirs ...string) string {
	path := path.Join(append([]string{m.dataDir}, dirs...)...)
	if err := os.MkdirAll(path, 0755); err != nil {
		panic(err)
	}
	return path
}

func (m *ConManager) GetByName(name string) (*Container, bool) {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	c, bool := m.containersByName[name]
	return c, bool
}

func (m *ConManager) GetByID(id string) (*Container, bool) {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	c, bool := m.containersByID[id]
	return c, bool
}

func (m *ConManager) ListContainers() []*Container {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	containers := make([]*Container, 0, len(m.containersByID))
	for _, c := range m.containersByID {
		containers = append(containers, c)
	}
	return containers
}

func (m *ConManager) removeContainer(c *Container) error {
	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	delete(m.containersByID, c.ID)
	delete(m.containersByName, c.Name)

	delete(c.manager.seccompCookies, c.seccompCookie)
	runtime.SetFinalizer(c, nil)
	c.c.Release()

	err := c.manager.db.DeleteContainer(c.ID)
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) DefaultUser() (string, error) {
	hostUser, err := m.host.GetUser()
	if err != nil {
		return "", err
	}
	return hostUser.Username, nil
}

type Container struct {
	ID    string
	Name  string
	Image ImageSpec
	dir   string

	builtin bool
	// state
	creating bool
	deleting bool

	c *lxc.Container

	agent   syncx.CondValue[*agent.Client]
	manager *ConManager
	mu      sync.RWMutex

	seccompCookie     uint64
	lastListeners     []agent.ProcListener
	autofwdDebounce   syncx.FuncDebounce
	lastAutofwdUpdate time.Time
}

func (c *Container) Agent() *agent.Client {
	return c.agent.Wait()
}

func (c *Container) Exec(cmd []string, opts lxc.AttachOptions, extraFd int) (int, error) {
	// no new fds in between
	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()

	// TODO cloexec safety
	// critical section
	if extraFd != 0 {
		// clear cloexec
		unix.FcntlInt(uintptr(extraFd), unix.F_SETFD, 0)
		defer unix.CloseOnExec(extraFd)
	}
	return c.c.RunCommandNoWait(cmd, opts)
}

func (c *Container) Running() bool {
	return c.c.Running()
}

func (c *Container) persist() error {
	if c.builtin {
		return nil
	}

	record := &ContainerRecord{
		ID:    c.ID,
		Name:  c.Name,
		Image: c.Image,

		Running:  c.Running(),
		Deleting: c.deleting,
	}
	logrus.WithField("record", record).Debug("persisting container")
	return c.manager.db.SetContainer(c.ID, record)
}

func deriveMacAddress(cid string) string {
	// hash of id
	h := sha256.Sum256([]byte(cid))
	// mark as locally administered
	h[0] |= 0x02
	// mark as unicast
	h[0] &= 0xfe
	// format
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", h[0], h[1], h[2], h[3], h[4], h[5])
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

	// rand seed
	rand.Seed(time.Now().UnixNano())

	// data dir
	cwd, err := os.Getwd()
	check(err)

	// connect to hcontrol (ownership taken by cmgr)
	if conf.C().DummyHcontrol {
		err = hclient.StartDummyServer()
		check(err)
	}
	logrus.Debug("connecting to hcontrol")
	hcontrolConn, err := net.Dial("tcp", conf.C().HcontrolIP+":"+strconv.Itoa(ports.ServiceHcontrol))
	check(err)
	hc, err := hclient.New(hcontrolConn)
	check(err)

	// start container manager
	mgr, err := NewConManager(cwd+"/data", hc)
	check(err)
	defer mgr.Close()
	mgr.Start()
	check(err)

	// services
	if conf.Debug() {
		go runPprof()
	}

	go runCliTest(mgr)

	// listen for signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, unix.SIGINT, unix.SIGTERM)
	select {
	case <-sigChan:
	case <-mgr.stopChan:
	}

	logrus.Info("shutting down")
}

func runCliTest(mgr *ConManager) {
	var err error
	defer func() {
		if err != nil {
			mgr.Close()
		}
	}()

	container, ok := mgr.GetByName("alpine")
	if !ok {
		// create
		fmt.Println("create")
		container, err = mgr.Create(CreateParams{
			Name: "alpine",
			User: "dragon",
			Image: ImageSpec{
				Distro:  "alpine",
				Version: "edge",
			},
			UserPassword: "test",
		})
		check(err)
	}

	fmt.Println("start")
	err = container.Start()
	check(err)
}

func main() {
	cmd := cmdContainerManager
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case cmdContainerManager:
		runContainerManager()
	default:
		panic("unknown command: " + cmd)
	}
}
