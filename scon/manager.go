package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/orbstack/macvirt/scon/agent/common"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/fsops"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/drm/sjwt"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol/htypes"
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
	lxcTmpDir          string
	seccompPolicyPaths [_seccompPolicyMax]string
	agentExe           *os.File
	kernelVersion      string
	fsOps              fsops.FSOps

	// state
	containersByID   map[string]*Container
	containersByName map[string]*Container
	containersMu     syncx.RWMutex
	stopping         atomic.Bool
	dockerProxy      *DockerProxy
	ctx              context.Context
	ctxCancel        context.CancelFunc

	// services
	db   *Database
	host *hclient.Client
	bpf  *bpf.GlobalBpfManager
	// TODO make this its own machine?
	k8sEnabled        bool
	k8sExposeServices bool
	uiEventDebounce   syncx.LeadingFuncDebounce
	uiInitContainers  sync.WaitGroup
	vmConfig          *vmtypes.VmConfig
	sconGuest         *SconGuestServer
	drm               *DrmMonitor
	wormhole          *WormholeManager

	// auto forward
	forwards   map[sysnet.ListenerKey]ForwardState
	forwardsMu syncx.Mutex

	// nfs
	nfsRoot        *NfsMirrorManager
	nfsContainers  *NfsMirrorManager
	nfsForMachines *NfsMirrorManager
	nfsForAll      NfsMirror
	fpll           *FpllManager

	// stop
	stopChan      chan struct{}
	earlyStopChan chan struct{}

	// network
	net *Network

	enableColorLogging bool
}

func NewConManager(dataDir string, hc *hclient.Client, initConfig *htypes.InitConfig) (*ConManager, error) {
	// tmp dir
	tmpDir, err := os.MkdirTemp("", AppName)
	if err != nil {
		return nil, err
	}

	// extract seccomp policy
	seccompPolicyPaths, err := writeSeccompPolicies(tmpDir)
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

	fsOps, err := fsops.NewForFS(conf.C().DataFsDir)
	if err != nil {
		return nil, fmt.Errorf("new fsops: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	mgr := &ConManager{
		dataDir:            dataDir,
		tmpDir:             tmpDir,
		lxcTmpDir:          lxcDir,
		seccompPolicyPaths: seccompPolicyPaths,
		agentExe:           agentExe,
		kernelVersion:      kernelVersion,
		fsOps:              fsOps,

		containersByID:   make(map[string]*Container),
		containersByName: make(map[string]*Container),
		ctx:              ctx,
		ctxCancel:        cancel,

		db:   db,
		host: hc,
		bpf:  bpfMgr,

		forwards: make(map[sysnet.ListenerKey]ForwardState),

		nfsRoot:        newNfsMirror(nfsDirRoot, true),
		nfsContainers:  newNfsMirror(nfsDirContainers, false),
		nfsForMachines: newNfsMirror(nfsDirForMachines, false),
		fpll:           NewFpllManager(),

		stopChan:      make(chan struct{}),
		earlyStopChan: make(chan struct{}),

		vmConfig: initConfig.VmConfig,
	}
	mgr.wormhole = NewWormholeManager(mgr)
	mgr.net = NewNetwork(mgr.subdir("network"), mgr.host, mgr.db, mgr)
	mgr.nfsForAll = NewMultiNfsMirror(mgr.nfsRoot, mgr.nfsForMachines)
	mgr.uiEventDebounce = *syncx.NewLeadingFuncDebounce(uitypes.UIEventDebounce, func() {
		// wait for initial starts
		mgr.uiInitContainers.Wait()

		infos, err := mgr.ListContainerInfos()
		if err != nil {
			logrus.WithError(err).Error("failed to get container infos")
			return
		}

		err = mgr.host.OnUIEvent(uitypes.UIEvent{
			Scon: &uitypes.SconEvent{
				CurrentMachines: infos,
			},
		})
	})

	// prevent UI from getting an event
	mgr.uiInitContainers.Add(1)

	return mgr, nil
}

func runOne(what string, fn func() error) {
	err := fn()
	if err != nil {
		logrus.WithError(err).Error(what + " failed")
	}
}

func writeWormholeExtraCerts(bundle string) error {
	// use securefs for absolute /nix symlink resolution
	file, err := securefs.OpenFile(mounts.WormholeRootfs, "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer file.Close()

	_, err = file.Write([]byte("\n" + bundle))
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}

	return nil
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

	// clean up leftover logs
	go runOne("cache cleanup", m.cleanupCaches)

	// certs - must be early because RPC server will allow creation, which uses HTTPS
	extraCerts, err := m.host.GetExtraCaCertificates()
	if err != nil {
		logrus.WithError(err).Error("Failed to get extra certs")
	}
	os.Setenv("SSL_CERT_DIR", "/run/certs")
	err = os.MkdirAll("/run/certs", 0700)
	if err != nil {
		return fmt.Errorf("mkdir certs: %w", err)
	}
	err = common.WriteCaCerts(securefs.Default(), "/run/certs", extraCerts)
	if err != nil {
		return fmt.Errorf("write certs: %w", err)
	}
	// write bundle too for nixos
	extraCertsBundle := strings.Join(extraCerts, "\n")
	err = os.WriteFile(mounts.HostExtraCerts, []byte(extraCertsBundle), 0644)
	if err != nil {
		return fmt.Errorf("write host certs: %w", err)
	}
	// and for wormhole
	err = writeWormholeExtraCerts(extraCertsBundle)
	if err != nil {
		return fmt.Errorf("write wormhole certs: %w", err)
	}

	// drm monitor
	drmMonitor := &DrmMonitor{
		conManager: m,
		//TODO identifiers
		//TODO version
		verifier: sjwt.NewVerifier(nil, drmtypes.AppVersion{}),

		initRestored: syncx.NewCondBool(),
	}
	m.drm = drmMonitor
	go runOne("DRM monitor", drmMonitor.Start)

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
	dockerMachine, err := m.GetByID(ContainerIDDocker)
	if err != nil {
		return err
	}
	go runOne("device monitor", m.runDeviceMonitor)
	// RPC only once other services are up
	go runOne("RPC server", func() error {
		return ListenScon(m, dockerMachine)
	})
	m.net.mdnsRegistry.domainproxy.dockerMachine = dockerMachine

	// RPC guest server must be started synchronously:
	// docker machine bind mounts /run/rc.sock (runc wrap server) which depends on scon guest
	err = ListenSconGuest(m)
	if err != nil {
		return fmt.Errorf("listen guest: %w", err)
	}

	// start all pending containers
	// do not alert the UI until all are started, to give it a consistent restored state
	// otherwise docker state will flicker
	for _, c := range pendingStarts {
		m.uiInitContainers.Add(1)
		go func(c *Container) {
			defer m.uiInitContainers.Done()

			err := c.Start()
			if err != nil {
				logrus.WithError(err).WithField("container", c.Name).Error("failed to start restored container")
			}
		}(c)
	}

	go runOne("internal RPC server", func() error {
		_, err := ListenSconInternal(m, drmMonitor)
		return err
	})
	go runOne("krpc initiator server", RunKrpcInitiator)

	// release the initial ui lock, now that container start jobs are pending, and trigger
	m.uiInitContainers.Done()
	m.uiEventDebounce.Call()

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

	return nil
}

func (m *ConManager) Close() error {
	// double channel close = panic, so need to protect this
	if m.stopping.Swap(true) {
		return nil
	}

	m.ctxCancel()

	close(m.earlyStopChan) // this acts as broadcast
	m.stopAll()

	logrus.Debug("finish cleanup")
	m.agentExe.Close()
	m.host.Close()
	m.net.Close()
	m.bpf.Close()
	m.nfsRoot.Close()
	m.nfsForMachines.Close()
	m.nfsContainers.Close()
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

func (m *ConManager) ListContainerInfos() ([]types.ContainerInfo, error) {
	containers := m.ListContainers()
	subvolumes, err := m.fsOps.GetSubvolumeSizes()
	if err != nil {
		return nil, fmt.Errorf("get subvolume sizes: %w", err)
	}

	infos := make([]types.ContainerInfo, 0, len(containers))
	for _, c := range containers {
		size, ok := subvolumes[c.quotaDir]
		var sizePtr *uint64
		if ok {
			sizePtr = &size
		}

		infos = append(infos, types.ContainerInfo{
			Record:   c.toRecord(),
			DiskSize: sizePtr,
		})
	}
	return infos, nil
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

	m.uiEventDebounce.Call()

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
	// look up default
	defaultID, err := m.db.GetDefaultContainerID()
	// db key doesn't exist on new installs
	if err != nil && !errors.Is(err, ErrKeyNotFound) {
		return nil, err
	}
	c, err := m.GetByID(defaultID)
	if err == nil {
		return c, nil
	}

	// failed to get default. either
	//   - there is no default ID set; or
	//   - there was, but it got deleted
	// look for a new container, in alphabetical order
	for _, c := range m.ListContainers() {
		if !c.builtin {
			// set it as default and return it
			err = m.SetDefaultContainer(c)
			if err != nil {
				return nil, err
			}

			return c, nil
		}
	}

	// none found
	return nil, ErrNoMachines
}

func (m *ConManager) SetDefaultContainer(c *Container) error {
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
