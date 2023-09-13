package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/sgclient"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/sirupsen/logrus"
)

const (
	dockerRefreshDebounce   = 100 * time.Millisecond
	dockerAPISocketUpstream = "/var/run/docker.sock"
)

type DockerAgent struct {
	mu       syncx.Mutex
	client   *dockerclient.Client
	Running  syncx.CondBool
	InitDone syncx.CondBool

	host *hclient.Client
	scon *sgclient.Client

	containerBinds         map[string][]string
	lastContainers         []dockertypes.ContainerSummaryMin // minimized struct to save memory
	hasRefreshedContainers bool
	lastNetworks           []dockertypes.Network
	lastVolumes            []*dockertypes.Volume

	lastImages     []*sgtypes.TaggedImage
	fullImageCache map[string]cachedImage

	// refreshing w/ debounce+diff ensures consistent snapshots
	containerRefreshDebounce syncx.FuncDebounce
	networkRefreshDebounce   syncx.FuncDebounce
	volumeRefreshDebounce    syncx.FuncDebounce
	imageRefreshDebounce     syncx.FuncDebounce
	uiEventDebounce          syncx.LeadingFuncDebounce
	pendingUIEntities        [uitypes.DockerEntityMax_]bool

	eventsConn io.Closer

	dirSyncMu       syncx.Mutex
	dirSyncListener net.Listener
	dirSyncJobs     map[uint64]chan error

	k8s   *K8sAgent
	pstub *PstubServer
}

func NewDockerAgent(isK8s bool) (*DockerAgent, error) {
	dockerAgent := &DockerAgent{
		// use default unix socket
		client: dockerclient.NewWithHTTP(&http.Client{
			// no timeout - we do event monitoring
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", dockerAPISocketUpstream)
				},
				// idle conns are ok here because we get frozen along with docker
				MaxIdleConns: 2,
			},
		}, nil),

		Running:  syncx.NewCondBool(),
		InitDone: syncx.NewCondBool(),

		fullImageCache: make(map[string]cachedImage),

		containerBinds: make(map[string][]string),
		dirSyncJobs:    make(map[uint64]chan error),
	}

	dockerAgent.containerRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshContainers()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh containers")
		}
	})
	dockerAgent.networkRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshNetworks()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh networks")
		}
	})
	dockerAgent.volumeRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshVolumes()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh volumes")
		}
	})
	dockerAgent.imageRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshImages()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh networks")
		}
	})
	dockerAgent.uiEventDebounce = *syncx.NewLeadingFuncDebounce(func() {
		err := dockerAgent.doSendUIEvent()
		if err != nil {
			logrus.WithError(err).Error("failed to send UI event")
		}

		// do not consider us fully started (freezable) until first event is sent.
		dockerAgent.InitDone.Set(true)
	}, uitypes.UIEventDebounce)

	if isK8s {
		dockerAgent.k8s = &K8sAgent{
			docker: dockerAgent,
		}
	}

	pstub, err := NewPstubServer()
	if err != nil {
		return nil, err
	}
	dockerAgent.pstub = pstub
	go func() {
		err := pstub.Serve()
		if err != nil {
			logrus.WithError(err).Error("pstub server failed")
		}
	}()

	return dockerAgent, nil
}

/*
 * Public RPC API
 */

func (a *AgentServer) DockerCheckIdle(_ None, reply *bool) error {
	// only includes running
	var containers []dockertypes.ContainerSummaryMin
	err := a.docker.client.Call("GET", "/containers/json", nil, &containers)
	if err != nil {
		return err
	}

	*reply = len(containers) == 0
	return nil
}

func (a *AgentServer) DockerHandleConn(fdxSeq uint64, _ *None) error {
	// receive fd
	file, err := a.fdx.RecvFile(fdxSeq)
	if err != nil {
		return err
	}
	defer file.Close()

	extConn, err := net.FileConn(file)
	if err != nil {
		return err
	}
	defer extConn.Close()

	// wait for docker
	a.docker.Running.Wait()

	// dial unix socket
	dockerConn, err := net.Dial("unix", dockerAPISocketUpstream)
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2SpTcpUnix(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}

func (a *AgentServer) DockerWaitStart(_ None, _ *None) error {
	a.docker.InitDone.Wait()
	return nil
}

/*
 * Private - Docker agent
 */

func (d *DockerAgent) PostStart() error {
	// wait for Docker API to start
	err := util.WaitForRunPathExist(dockerAPISocketUpstream)
	if err != nil {
		return err
	}

	// fix very rare race: socket file is created on 'bind' but refuses connections until 'listen', causing event monitor to break
	// TODO edit json and use docker fd://* syntax instead. zero race and more flexible
	err = util.WaitForSocketConnectible(dockerAPISocketUpstream)
	if err != nil {
		return err
	}

	// check for migration flag
	if origConfigJson, err := os.ReadFile(DockerNetMigrationFlag); err == nil {
		// this is the signal that we need to migrate
		err = d.migrateConflictNetworks(origConfigJson)
		if err != nil {
			return err
		}

		// great, migration successful, flag deleted to prevent recursion.
		// enter PostStart again to continue
		return d.PostStart()
	}

	d.Running.Set(true)

	// no point in doing this async. main goroutine exits after PostStart
	hConn, err := net.Dial("unix", mounts.HcontrolSocket)
	if err != nil {
		return err
	}
	d.host, err = hclient.New(hConn)
	if err != nil {
		return err
	}

	sConn, err := net.Dial("unix", mounts.SconGuestSocket)
	if err != nil {
		return err
	}
	d.scon, err = sgclient.New(sConn)
	if err != nil {
		return err
	}

	// start docker event monitor
	go func() {
		err = d.monitorEvents()
		if err != nil {
			logrus.WithError(err).Error("failed to monitor Docker events")
		}
	}()

	// send kubeconfig to host
	if d.k8s != nil {
		err = d.k8s.PostStart()
		if err != nil {
			return fmt.Errorf("k8s post start: %w", err)
		}
	}

	return nil
}

func (d *DockerAgent) killContainers() error {
	// kill containers for faster shutdown. min docker shutdown timeout = 6s (1 + 5s)
	// good programs should be safe to kill in case of power loss anyway, as long as we do it atomically like this
	// let docker daemon shut down cleanly due to bolt DBs and image db
	logrus.Debug("killing containers")

	// cgroup kill is recursive so no need to listdir
	err := os.WriteFile("/sys/fs/cgroup/docker/cgroup.kill", []byte("1"), 0644)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// ENOENT: it may already be dead, or not running
		return err
	}

	err = os.WriteFile("/sys/fs/cgroup/kubepods/cgroup.kill", []byte("1"), 0644)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

func (d *DockerAgent) OnStop() error {
	logrus.Debug("stopping docker event monitor")
	if d.eventsConn != nil {
		d.eventsConn.Close()
	}

	err := d.killContainers()
	if err != nil {
		return err
	}

	return nil
}

func (d *DockerAgent) triggerUIEvent(entity uitypes.DockerEntity) {
	logrus.Info("ev: queue ", entity)
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pendingUIEntities[entity] = true
	d.uiEventDebounce.Trigger()
}

func (d *DockerAgent) triggerAllUIEvents() {
	logrus.Info("ev: TA")
	d.mu.Lock()
	defer d.mu.Unlock()

	for i := range d.pendingUIEntities {
		d.pendingUIEntities[i] = true
	}
	d.uiEventDebounce.Trigger()
}

func (d *DockerAgent) doSendUIEvent() error {
	// can be triggered by UI before dockerd starts
	logrus.Info("do send 1")
	d.Running.Wait()
	logrus.Info("do send 2")

	d.mu.Lock()
	defer d.mu.Unlock()
	logrus.Info("do send 3")

	event := uitypes.DockerEvent{}

	if d.pendingUIEntities[uitypes.DockerEntityContainer] {
		containers, err := d.client.ListContainers(true /*all*/)
		if err != nil {
			return err
		}
		event.CurrentContainers = containers
	}

	if d.pendingUIEntities[uitypes.DockerEntityVolume] {
		volumes, err := d.client.ListVolumes()
		if err != nil {
			return err
		}
		event.CurrentVolumes = volumes
	}

	if d.pendingUIEntities[uitypes.DockerEntityImage] {
		images, err := d.client.ListImages()
		if err != nil {
			return err
		}
		event.CurrentImages = images
	}

	logrus.Info("do send 4")
	err := d.host.OnUIEvent(uitypes.UIEvent{
		Docker: &event,
	})
	if err != nil {
		return err
	}

	logrus.Info("do send 5")
	for i := range d.pendingUIEntities {
		d.pendingUIEntities[i] = false
	}
	return nil
}

func (d *DockerAgent) monitorEvents() error {
	logrus.Info("get ev 1")
	eventsConn, err := d.client.StreamRead("GET", "/events", nil)
	if err != nil {
		return err
	}
	defer eventsConn.Close()
	d.eventsConn = eventsConn

	// kick an initial refresh
	d.containerRefreshDebounce.Call()
	d.networkRefreshDebounce.Call()
	d.volumeRefreshDebounce.Call()
	d.imageRefreshDebounce.Call()
	// also kick all initial UI events for menu bar bg start
	d.triggerAllUIEvents()
	logrus.Info("get ev 2")

	dec := json.NewDecoder(eventsConn)
	for {
		var event dockertypes.Event
		err := dec.Decode(&event)
		if err != nil {
			// EOF = Docker daemon stopped, or conn closed by PreStop
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
				return nil
			} else {
				return fmt.Errorf("decode json: %w", err)
			}
		}

		if conf.Debug() {
			logrus.WithField("event", event).Debug("engine event")
		}
		switch event.Type {
		case "container":
			switch event.Action {
			case "create", "start", "die", "destroy":
				d.triggerUIEvent(uitypes.DockerEntityContainer)
				d.containerRefreshDebounce.Call()
				// also need to trigger networks refresh, because networks depends on active containers
				d.networkRefreshDebounce.Call()
			}

		case "volume":
			switch event.Action {
			// include mount, unmount because UI shows used/unused
			case "create", "destroy", "mount", "unmount":
				d.triggerUIEvent(uitypes.DockerEntityVolume)
				d.volumeRefreshDebounce.Call()
			}

		case "image":
			// no UI event for images: unnecessary
			switch event.Action {
			case "delete", "import", "load", "pull", "tag", "untag":
				// TODO clear full image cache on tag/untag. event doesn't contain image ID
				d.triggerUIEvent(uitypes.DockerEntityImage)
				d.imageRefreshDebounce.Call()
			}

		case "network":
			switch event.Action {
			// "connect" and "disconnect" for dynamic bridge creation depending on active containers
			case "create", "destroy", "connect", "disconnect":
				// we only care about bridges
				if event.Actor.Attributes.Type == "bridge" {
					d.networkRefreshDebounce.Call()
				}
			}
		}
	}
}

func (a *AgentServer) DockerGuiReportStarted(_ None, _ *None) error {
	logrus.Info("ev: report")
	a.docker.triggerAllUIEvents()
	return nil
}
