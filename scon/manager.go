package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/orbstack/macvirt/scon/agent/common"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/drm/sjwt"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	ErrStopping        = errors.New("manager is stopping")
	ErrMachineNotFound = errors.New("machine not found")
)

type ConManager struct {
	// config
	dataDir            string
	tmpDir             string
	lxcDir             string
	seccompPolicyPaths [_seccompPolicyMax]string
	agentExe           *os.File
	kernelVersion      string

	// state
	containersByID   map[string]*Container
	containersByName map[string]*Container
	containersMu     syncx.RWMutex
	stopping         bool
	dockerProxy      *DockerProxy

	// services
	db             *Database
	host           *hclient.Client
	hostNfsMounted bool
	bpf            *bpf.GlobalBpfManager
	// TODO make this its own machine?
	k8sEnabled bool

	// auto forward
	forwards   map[sysnet.ListenerKey]ForwardState
	forwardsMu syncx.Mutex

	// nfs
	nfsRoot        *NfsMirrorManager
	nfsForMachines *NfsMirrorManager
	nfsForAll      NfsMirror

	// stop
	stopChan      chan struct{}
	earlyStopChan chan struct{}

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
	seccompPolicyPaths, err := writeSeecompPolicies(tmpDir)
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

	bpfMgr, err := bpf.NewGlobalBpfManager()
	if err != nil {
		return nil, err
	}

	var uname unix.Utsname
	err = unix.Uname(&uname)
	if err != nil {
		return nil, err
	}
	kernelVersion := string(uname.Release[:bytes.IndexByte(uname.Release[:], 0)])

	mgr := &ConManager{
		dataDir:            dataDir,
		tmpDir:             tmpDir,
		lxcDir:             lxcDir,
		seccompPolicyPaths: seccompPolicyPaths,
		agentExe:           agentExe,
		kernelVersion:      kernelVersion,

		containersByID:   make(map[string]*Container),
		containersByName: make(map[string]*Container),

		db:   db,
		host: hc,
		bpf:  bpfMgr,

		forwards: make(map[sysnet.ListenerKey]ForwardState),

		nfsRoot:        newNfsMirror(nfsDirRoot, true),
		nfsForMachines: newNfsMirror(nfsDirForMachines, false),

		stopChan:      make(chan struct{}),
		earlyStopChan: make(chan struct{}),
	}
	mgr.net = NewNetwork(mgr.subdir("network"), mgr.host)
	mgr.nfsForAll = NewMultiNfsMirror(mgr.nfsRoot, mgr.nfsForMachines)

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
	_ = os.RemoveAll(m.subdir("images"))

	// network
	err := m.net.Start()
	if err != nil {
		return err
	}

	// bpf
	err = m.bpf.Load(ifVmnetMachine)
	if err != nil {
		return err
	}

	// restore first
	pendingStarts, err := m.restoreContainers()
	if err != nil {
		return err
	}

	// clean up leftover logs and rootfs
	go runOne("cache cleanup", m.cleanupCaches)

	// certs - must be early because RPC server will allow creation, which uses HTTPS
	extraCerts, err := m.host.GetExtraCaCertificates()
	if err != nil {
		logrus.WithError(err).Error("Failed to get extra certs")
	}
	err = common.WriteCaCerts(securefs.Default(), "/etc/ssl/certs", extraCerts)
	if err != nil {
		return fmt.Errorf("write certs: %w", err)
	}
	// write bundle too for nixos
	err = os.WriteFile(mounts.ExtraCerts, []byte(strings.Join(extraCerts, "\n")), 0644)
	if err != nil {
		return err
	}

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
	go runOne("device monitor", m.runDeviceMonitor)
	// RPC only once other services are up
	go runOne("RPC server", func() error {
		return ListenScon(m)
	})
	go runOne("guest RPC server", func() error {
		return ListenSconGuest(m)
	})

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
		_, err := ListenSconInternal(m, drmMonitor)
		return err
	})
	go runOne("krpc initiator server", RunKrpcInitiator)

	logrus.Info("started")
	return nil
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
				logrus.WithError(err).WithField("file", f.Name()).Error("failed to remove orphaned log file")
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
				logrus.WithError(err).WithField("container", f.Name()).Error("failed to remove orphaned rootfs")
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
	m.bpf.Close()
	m.nfsRoot.Close()
	m.nfsForMachines.Close()
	close(m.stopChan)          // this acts as broadcast
	_ = os.RemoveAll(m.tmpDir) // seccomp and lxc
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

			err := c.stopForManagerShutdown()
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

func (m *ConManager) getByNameLocked(name string) (*Container, error) {
	c, ok := m.containersByName[name]
	if !ok {
		return nil, ErrMachineNotFound
	}
	return c, nil
}

func (m *ConManager) GetByName(name string) (*Container, error) {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	return m.getByNameLocked(name)
}

func (m *ConManager) GetByID(id string) (*Container, error) {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	c, ok := m.containersByID[id]
	if !ok {
		return nil, ErrMachineNotFound
	}
	return c, nil
}

func (m *ConManager) ListContainers() []*Container {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	containers := make([]*Container, 0, len(m.containersByID))
	for _, c := range m.containersByID {
		containers = append(containers, c)
	}
	slices.SortFunc(containers, func(a, b *Container) int {
		return cmp.Compare(a.Name, b.Name)
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
	for _, alias := range c.Aliases {
		delete(m.containersByName, alias)
	}

	runtime.SetFinalizer(c, nil)
	_ = c.lxc.Release()

	err := c.manager.db.DeleteContainer(c.ID)
	if err != nil {
		return err
	}

	return nil
}

func (m *ConManager) defaultUser() (string, error) {
	// use explicit default from DB if we have it
	defaultUser, err := m.db.GetDefaultUsername()
	if err == nil && defaultUser != "" {
		return defaultUser, nil
	}

	hostUser, err := m.host.GetUser()
	if err != nil {
		return "", err
	}
	return hostUser.Username, nil
}

func (m *ConManager) SetDefaultUsername(username string) error {
	return m.db.SetDefaultUsername(username)
}

func (m *ConManager) GetDefaultContainer() (*Container, bool, error) {
	// look up default ID first
	id, err := m.db.GetDefaultContainerID()
	isExplicit := true
	defaultID := id
	if err != nil || id == "" || id == containerIDLastUsed {
		isExplicit = false

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

	// we have an ID now, so look it up
	c, err := m.GetByID(id)
	if err != nil && id != "" {
		// ID no longer exists.
		// pick first non-builtin container
		isExplicit = false
		for _, c := range m.ListContainers() {
			if c.builtin {
				continue
			}

			id = c.ID
			break
		}

		c, err = m.GetByID(id)
	}
	if err != nil {
		return nil, false, ErrNoMachines
	}
	// if we had a non-last-used default ID, and it no longer exists, make this the new default
	if defaultID != "" && defaultID != containerIDLastUsed {
		err = m.db.SetDefaultContainerID(id)
		if err != nil {
			return nil, false, err
		}
	}

	return c, isExplicit, nil
}

func (m *ConManager) SetDefaultContainer(c *Container) error {
	if c == nil {
		// nil = last-used
		return m.db.SetDefaultContainerID(containerIDLastUsed)
	}

	return m.db.SetDefaultContainerID(c.ID)
}

func (m *ConManager) ForEachContainer(fn func(c *Container) error) error {
	m.containersMu.RLock()
	defer m.containersMu.RUnlock()

	errs := make([]error, 0)
	for _, c := range m.containersByID {
		err := fn(c)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// mount after, so inode doesn't change
func (m *ConManager) onHostNfsMounted() error {
	// dupe is ok - we'll have to remount if inode changed
	m.hostNfsMounted = true

	hostUser, err := m.host.GetUser()
	if err != nil {
		return err
	}

	return m.ForEachContainer(func(c *Container) error {
		if !c.Running() {
			return nil
		}

		return bindMountNfsRoot(c, "/mnt/machines", hostUser.HomeDir+"/"+mounts.NfsDirName)
	})
}

func (m *ConManager) getAndWriteCerts(fs *securefs.FS, destDir string) error {
	extraCaCerts, err := m.host.GetExtraCaCertificates()
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}

	err = common.WriteCaCerts(fs, destDir, extraCaCerts)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
}
