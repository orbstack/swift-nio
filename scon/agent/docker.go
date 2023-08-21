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
	"slices"
	"time"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/sgclient"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

const (
	dockerRefreshDebounce = 100 * time.Millisecond
	// TODO: skip debounce when GUI action in progress
	dockerUIEventDebounce = 50 * time.Millisecond
)

type DockerAgent struct {
	mu      syncx.Mutex
	client  *dockerclient.Client
	Running syncx.CondBool

	host *hclient.Client
	scon *sgclient.Client

	containerBinds map[string][]string
	lastContainers []dockertypes.ContainerSummaryMin // minimized struct to save memory
	lastNetworks   []dockertypes.Network

	lastImages     []*dockertypes.FullImage
	fullImageCache map[string]*dockertypes.FullImage

	// refreshing w/ debounce+diff ensures consistent snapshots
	containerRefreshDebounce syncx.FuncDebounce
	networkRefreshDebounce   syncx.FuncDebounce
	imageRefreshDebounce     syncx.FuncDebounce
	uiEventDebounce          syncx.FuncDebounce
	pendingUIEntities        []dockertypes.UIEntity

	eventsConn io.Closer

	dirSyncMu       syncx.Mutex
	dirSyncListener net.Listener
	dirSyncJobs     map[uint64]chan error

	k8s *K8sAgent
}

func NewDockerAgent(isK8s bool) *DockerAgent {
	dockerAgent := &DockerAgent{
		// use default unix socket
		client: dockerclient.NewWithHTTP(&http.Client{
			// no timeout - we do event monitoring
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", "/var/run/docker.sock")
				},
				// idle conns are ok here because we get frozen along with docker
				MaxIdleConns: 2,
			},
		}, nil),

		Running: syncx.NewCondBool(),

		fullImageCache: make(map[string]*dockertypes.FullImage),

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
	dockerAgent.imageRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshImages()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh networks")
		}
	})
	dockerAgent.uiEventDebounce = syncx.NewFuncDebounce(dockerUIEventDebounce, func() {
		err := dockerAgent.doSendUIEvent()
		if err != nil {
			logrus.WithError(err).Error("failed to send UI event")
		}
	})

	if isK8s {
		dockerAgent.k8s = &K8sAgent{
			docker: dockerAgent,
		}
	}

	return dockerAgent
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
	dockerConn, err := net.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2SpTcpUnix(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}

func (a *AgentServer) DockerWaitStart(_ None, _ *None) error {
	a.docker.Running.Wait()
	return nil
}

/*
 * Private - Docker agent
 */

func (d *DockerAgent) PostStart() error {
	// wait for Docker API to start
	err := util.WaitForRunPathExist("/var/run/docker.sock")
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

	// start docker event monitor
	go func() {
		hConn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			logrus.WithError(err).Error("failed to connect to hcontrol")
			return
		}
		d.host, err = hclient.New(hConn)
		if err != nil {
			logrus.WithError(err).Error("failed to create hclient")
			return
		}

		sConn, err := net.Dial("unix", mounts.SconGuestSocket)
		if err != nil {
			logrus.WithError(err).Error("failed to connect to scon guest")
			return
		}
		d.scon, err = sgclient.New(sConn)
		if err != nil {
			logrus.WithError(err).Error("failed to create scon guest client")
			return
		}

		err = d.monitorEvents()
		if err != nil {
			logrus.WithError(err).Error("failed to monitor Docker events")
		}
	}()

	// send kubeconfig to host
	if d.k8s != nil {
		go func() {
			err := d.k8s.WaitAndSendKubeConfig()
			if err != nil {
				logrus.WithError(err).Error("failed to send kubeconfig")
			}
		}()
	}

	return nil
}

func (d *DockerAgent) killContainers() error {
	// kill containers for faster shutdown. min docker shutdown timeout = 6s (1 + 5s)
	// good programs should be safe to kill in case of power loss anyway, as long as we do it atomically like this
	// let docker daemon shut down cleanly due to bolt DBs and image db
	logrus.Debug("killing containers")
	cgroups, err := os.ReadDir("/sys/fs/cgroup/docker")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// no cgroups, no containers
			return nil
		} else {
			return err
		}
	}
	for _, cgroup := range cgroups {
		if !cgroup.IsDir() {
			continue
		}

		err = os.WriteFile("/sys/fs/cgroup/docker/"+cgroup.Name()+"/cgroup.kill", []byte("1"), 0644)
		if err != nil {
			// ENOENT: it may already be dead
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
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

func (d *DockerAgent) triggerUIEvent(entity dockertypes.UIEntity) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !slices.Contains(d.pendingUIEntities, entity) {
		d.pendingUIEntities = append(d.pendingUIEntities, entity)
	}
	d.uiEventDebounce.Call()
}

func (d *DockerAgent) doSendUIEvent() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	event := dockertypes.UIEvent{
		Changed: d.pendingUIEntities,
	}
	logrus.WithField("event", event).Debug("sending UI event")
	err := d.host.OnDockerUIEvent(&event)
	if err != nil {
		return err
	}

	d.pendingUIEntities = nil
	return nil
}

func (d *DockerAgent) monitorEvents() error {
	eventsConn, err := d.client.StreamRead("GET", "/events", nil)
	if err != nil {
		return err
	}
	defer eventsConn.Close()
	d.eventsConn = eventsConn

	// kick an initial refresh
	d.containerRefreshDebounce.Call()
	d.networkRefreshDebounce.Call()
	d.imageRefreshDebounce.Call()
	// also kick all initial UI events for menu bar bg start
	d.triggerUIEvent(dockertypes.UIEventContainer)
	d.triggerUIEvent(dockertypes.UIEventVolume)
	d.triggerUIEvent(dockertypes.UIEventImage)

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
				d.triggerUIEvent(dockertypes.UIEventContainer)
				d.containerRefreshDebounce.Call()
				// also need to trigger networks refresh, because networks depends on active containers
				d.networkRefreshDebounce.Call()
			}

		case "volume":
			switch event.Action {
			// include mount, unmount because UI shows used/unused
			case "create", "destroy", "mount", "unmount":
				d.triggerUIEvent(dockertypes.UIEventVolume)
			}

		case "image":
			// no UI event for images: unnecessary
			switch event.Action {
			case "delete", "import", "load", "pull", "tag", "untag":
				// TODO clear full image cache on tag/untag. event doesn't contain image ID
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
