package main

import (
	"os"
	"path"
	"runtime"
	"strings"
	"sync"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/sirupsen/logrus"
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

		stopChan: make(chan struct{}),
	}
	mgr.net = NewNetwork(mgr.subdir("network"))

	return mgr, nil
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
	go func() {
		err := m.serveSeccomp()
		if err != nil {
			logrus.WithError(err).Error("failed to start seccomp server")
		}
	}()

	// restore and start!
	err = m.restoreContainers()
	if err != nil {
		return err
	}

	// Docker proxy
	go func() {
		err := m.runDockerProxy()
		if err != nil {
			logrus.WithError(err).Error("failed to start Docker proxy")
		}
	}()

	// clean up leftover logs and rootfs
	go func() {
		err := m.cleanupCaches()
		if err != nil {
			logrus.WithError(err).Error("failed to clean up caches")
		}
	}()

	// services
	go func() {
		err := runSconServer(m)
		if err != nil {
			logrus.WithError(err).Error("failed to start scon server")
		}
	}()
	go func() {
		err := m.listenSSH(conf.C().SSHListen)
		if err != nil {
			logrus.WithError(err).Error("failed to start SSH server")
		}
	}()

	// periodic tasks
	go m.runAutoForwardGC()

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
	if m.stopping {
		return nil
	}

	m.stopping = true
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
