package main

import (
	"errors"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm/sjwt"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

type ConManager struct {
	// config
	dataDir           string
	tmpDir            string
	lxcDir            string
	seccompPolicyPath string
	seccompProxySock  string
	agentExe          *os.File

	// state
	containersByID   map[string]*Container
	containersByName map[string]*Container
	containersMu     sync.RWMutex
	seccompCookies   map[uint64]*Container
	stopping         bool
	dockerProxy      *DockerProxy

	// services
	db   *Database
	host *hclient.Client

	// auto forward
	forwards   map[agent.ProcListener]ForwardState
	forwardsMu sync.Mutex

	// nfs
	nfsMu sync.Mutex

	// stop
	stopChan          chan struct{}
	earlyStopChan     chan struct{}
	pendingVMShutdown bool

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

	// must stay same or we get duplicate containers across restarts
	lxcDir := "/tmp/scon-lxc"
	err = os.MkdirAll(lxcDir, 0755)
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

	agentExePath, err := findAgentExe()
	if err != nil {
		return nil, err
	}
	agentExe, err := os.Open(agentExePath)
	if err != nil {
		return nil, err
	}

	mgr := &ConManager{
		dataDir:           dataDir,
		tmpDir:            tmpDir,
		lxcDir:            lxcDir,
		seccompPolicyPath: seccompPolicyPath,
		seccompProxySock:  seccompProxySock,
		agentExe:          agentExe,

		containersByID:   make(map[string]*Container),
		containersByName: make(map[string]*Container),
		seccompCookies:   make(map[uint64]*Container),

		db:   db,
		host: hc,

		forwards: make(map[agent.ProcListener]ForwardState),

		stopChan:      make(chan struct{}),
		earlyStopChan: make(chan struct{}),
	}
	mgr.net = NewNetwork(mgr.subdir("network"))

	return mgr, nil
}

func runOne(what string, fn func() error) {
	err := fn()
	if err != nil {
		logrus.WithError(err).Error(what + " failed")
	}
}

func (m *ConManager) Start() error {
	// delete leftover image cache
	// TODO actually cache images
	os.RemoveAll(m.subdir("images"))

	// network
	err := m.net.Start()
	if err != nil {
		return err
	}

	// essential services for starting containers
	go runOne("seccomp server", m.serveSeccomp)

	// restore first
	pendingStarts, err := m.restoreContainers()
	if err != nil {
		return err
	}

	// clean up leftover logs and rootfs
	go runOne("cache cleanup", m.cleanupCaches)

	// services
	go runOne("SSH server", func() error {
		cleanup, err := m.runSSHServer(conf.C().SSHListenIP4, conf.C().SSHListenIP6)
		if err != nil {
			return err
		}
		// stop early to avoid returning "machine manager is stopping"
		defer cleanup()
		<-m.earlyStopChan
		return nil
	})
	// this one must be synchronous since post-start hook calls it
	err = m.startDockerProxy()
	if err != nil {
		return err
	}
	go runOne("Docker NFS manager", m.runDockerNFS)
	// RPC only once other services are up
	go runOne("RPC server", func() error {
		return runSconServer(m)
	})

	// periodic tasks
	go m.runAutoForwardGC()

	// start all pending containers
	for _, c := range pendingStarts {
		go func(c *Container) {
			err := c.Start()
			if err != nil {
				logrus.WithError(err).WithField("container", c.Name).Error("failed to start restored container")
			}
		}(c)
	}

	// drm monitor
	drmMonitor := &DrmMonitor{
		conManager: m,
		//TODO identifiers
		//TODO version
		verifier: sjwt.NewVerifier(nil, drmtypes.AppVersion{}),
	}
	go runOne("DRM monitor", drmMonitor.Start)
	go runOne("internal RPC server", func() error {
		_, err := ListenSconInternal(drmMonitor)
		return err
	})

	logrus.Info("started")
	return err
}

func (m *ConManager) cleanupCaches() error {
	// clean up logs
	logDir := m.subdir("logs")
	files, err := os.ReadDir(logDir)
	if err != nil {
		return err
	}
	for _, f := range files {
		parts := strings.Split(f.Name(), ".")
		id := parts[0]
		if _, ok := m.containersByID[id]; !ok {
			err = os.Remove(path.Join(logDir, f.Name()))
			if err != nil {
				return err
			}
		}
	}

	// clean up rootfs
	containersDir := m.subdir("containers")
	files, err = os.ReadDir(containersDir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if _, ok := m.containersByID[f.Name()]; !ok {
			err = deleteRootfs(path.Join(containersDir, f.Name()))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (m *ConManager) Close() error {
	// TODO need to lock here
	if m.stopping {
		return nil
	}

	m.stopping = true
	close(m.earlyStopChan) // this acts as broadcast
	m.stopAll()

	logrus.Debug("finish cleanup")
	m.agentExe.Close()
	m.host.Close()
	m.net.Close()
	close(m.stopChan)      // this acts as broadcast
	os.RemoveAll(m.tmpDir) // seccomp and lxc
	return nil
}

func (m *ConManager) stopAll() {
	m.containersMu.Lock()

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
	m.containersMu.Unlock()
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
	slices.SortFunc(containers, func(a, b *Container) bool {
		return a.Name < b.Name
	})
	return containers
}

func (m *ConManager) CountNonBuiltinContainers() int {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	count := 0
	for _, c := range m.containersByID {
		if !c.builtin {
			count++
		}
	}
	return count
}

func (m *ConManager) removeContainer(c *Container) error {
	m.containersMu.Lock()
	defer m.containersMu.Unlock()

	delete(m.containersByID, c.ID)
	delete(m.containersByName, c.Name)

	delete(c.manager.seccompCookies, c.seccompCookie)
	runtime.SetFinalizer(c, nil)
	c.lxc.Release()

	err := c.manager.db.DeleteContainer(c.ID)
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) defaultUser() (string, error) {
	hostUser, err := m.host.GetUser()
	if err != nil {
		return "", err
	}
	return hostUser.Username, nil
}

func (m *ConManager) GetDefaultContainer() (*Container, error) {
	id, err := m.db.GetDefaultContainerID()
	defaultID := id
	if err != nil || id == "" || id == containerIDLastUsed {
		// fallback to last-used, or if explicitly set
		id, err = m.db.GetLastContainerID()
		if err != nil {
			// pick first non-builtin container
			for _, c := range m.ListContainers() {
				if c.builtin {
					continue
				}

				id = c.ID
				break
			}
		}
	}

	c, ok := m.GetByID(id)
	if !ok && id != "" {
		// pick first non-builtin container
		for _, c := range m.ListContainers() {
			if c.builtin {
				continue
			}

			id = c.ID
			break
		}

		c, ok = m.GetByID(id)
	}
	if !ok {
		return nil, errors.New("no machines found")
	}
	// if we had a non-last-used default ID, and it no longer exists, make this the new default
	if defaultID != "" && defaultID != containerIDLastUsed {
		err = m.db.SetDefaultContainerID(id)
		if err != nil {
			return nil, err
		}
	}

	return c, nil
}

func (m *ConManager) SetDefaultContainer(c *Container) error {
	if c == nil {
		// nil = last-used
		return m.db.SetDefaultContainerID(containerIDLastUsed)
	}

	return m.db.SetDefaultContainerID(c.ID)
}

func (m *ConManager) HasExplicitDefaultContainer() (bool, error) {
	id, err := m.db.GetDefaultContainerID()
	if err != nil {
		return false, err
	}

	return id != containerIDLastUsed, nil
}
