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
	"strings"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/hclient"
	_ "github.com/orbstack/macvirt/scon/mdns/mdnsgob"
	"github.com/orbstack/macvirt/scon/sgclient"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

const (
	dockerRefreshDebounce   = 100 * time.Millisecond
	dockerAPISocketUpstream = "/var/run/docker.sock"

	// matches mDNSResponder timeout
	mdnsProxyTimeout = 5 * time.Second
	kubeDnsUpstream  = netconf.K8sCorednsIP4 + ":53"
)

type DockerAgent struct {
	mu       syncx.Mutex
	client   *dockerclient.Client
	Running  syncx.CondBool
	InitDone syncx.CondBool

	agent *AgentServer

	host *hclient.Client
	scon *sgclient.Client

	wakeRefs atomic.Int32

	containerBinds map[string][]string
	lastContainers []dockertypes.ContainerSummaryMin // minimized struct to save memory
	lastNetworks   []dockertypes.Network
	lastVolumes    []*dockertypes.Volume

	lastImages     []*sgtypes.TaggedImage
	fullImageCache map[string]cachedImage

	// refreshing w/ debounce+diff ensures consistent snapshots
	containerRefreshDebounce syncx.LeadingFuncDebounce
	networkRefreshDebounce   syncx.LeadingFuncDebounce
	volumeRefreshDebounce    syncx.LeadingFuncDebounce
	imageRefreshDebounce     syncx.LeadingFuncDebounce
	uiEventDebounce          syncx.LeadingFuncDebounce
	pendingUIEntities        [uitypes.DockerEntityMax_]bool

	eventsConn io.Closer

	dirSyncMu       syncx.Mutex
	dirSyncListener net.Listener
	dirSyncJobs     map[uint64]chan error

	k8s             *K8sAgent
	pstub           *PstubServer
	tlsProxy        *tlsProxy
	tlsProxyEnabled bool
}

func NewDockerAgent(isK8s bool, isTls bool) (*DockerAgent, error) {
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

		tlsProxyEnabled: isTls,
	}

	dockerAgent.containerRefreshDebounce = *syncx.NewLeadingFuncDebounce(dockerRefreshDebounce, func() {
		dockerAgent.incWakeRef()
		defer dockerAgent.decWakeRef()

		err := dockerAgent.refreshContainers()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh containers")
		}
	})
	dockerAgent.networkRefreshDebounce = *syncx.NewLeadingFuncDebounce(dockerRefreshDebounce, func() {
		dockerAgent.incWakeRef()
		defer dockerAgent.decWakeRef()

		err := dockerAgent.refreshNetworks()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh networks")
		}
	})
	dockerAgent.volumeRefreshDebounce = *syncx.NewLeadingFuncDebounce(dockerRefreshDebounce, func() {
		dockerAgent.incWakeRef()
		defer dockerAgent.decWakeRef()

		err := dockerAgent.refreshVolumes()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh volumes")
		}
	})
	dockerAgent.imageRefreshDebounce = *syncx.NewLeadingFuncDebounce(dockerRefreshDebounce, func() {
		dockerAgent.incWakeRef()
		defer dockerAgent.decWakeRef()

		err := dockerAgent.refreshImages()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh networks")
		}
	})
	dockerAgent.uiEventDebounce = *syncx.NewLeadingFuncDebounce(uitypes.UIEventDebounce, func() {
		dockerAgent.incWakeRef()
		defer dockerAgent.decWakeRef()

		err := dockerAgent.doSendUIEvent()
		if err != nil {
			logrus.WithError(err).Error("failed to send UI event")
		}

		// do not consider us fully started (freezable) until first event is sent.
		dockerAgent.InitDone.Set(true)
	})

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

	hConn, err := net.Dial("unix", mounts.HcontrolSocket)
	if err != nil {
		return nil, err
	}
	dockerAgent.host, err = hclient.New(hConn)
	if err != nil {
		return nil, err
	}

	// start tls proxy
	dockerAgent.tlsProxy, err = newTLSProxy(dockerAgent.host)
	if err != nil {
		return nil, err
	}
	err = dockerAgent.tlsProxy.Start()
	if err != nil {
		return nil, err
	}

	return dockerAgent, nil
}

/*
 * Public RPC API
 */

func (a *AgentServer) DockerCheckIdle(_ None, reply *bool) error {
	// an early predicate check
	if a.docker.wakeRefs.Load() > 0 {
		*reply = false
		return nil
	}

	// only includes running
	var containers []dockertypes.ContainerSummaryMin
	err := a.docker.client.Call("GET", "/containers/json", nil, &containers)
	if err != nil {
		return err
	}

	*reply = len(containers) == 0

	// a late predicate check in case of race
	if a.docker.wakeRefs.Load() > 0 {
		*reply = false
	}

	return nil
}

func (a *AgentServer) DockerDialSocket(_ None, reply *uint64) error {
	// wait for docker
	a.docker.Running.Wait()

	// dial unix socket
	dockerConn, err := net.Dial("unix", dockerAPISocketUpstream)
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	// send file
	file, err := dockerConn.(*net.UnixConn).File()
	if err != nil {
		return err
	}
	defer file.Close()
	seq, err := a.fdx.SendFile(file)
	if err != nil {
		return err
	}

	*reply = seq
	return nil
}

func (a *AgentServer) DockerWaitStart(_ None, _ *None) error {
	a.docker.InitDone.Wait()
	return nil
}

func (a *AgentServer) DockerQueryKubeDns(q dns.Question, rrs *[]dns.RR) error {
	ctx, cancel := context.WithTimeout(context.Background(), mdnsProxyTimeout)
	defer cancel()

	// forward to kubedns (static IP)
	msg := new(dns.Msg)
	msg.SetQuestion(q.Name, q.Qtype)
	msg.RecursionDesired = false // mDNS
	// this value is from dig
	msg.SetEdns0(1232, false)
	reply, err := dns.ExchangeContext(ctx, msg, kubeDnsUpstream)
	if err != nil {
		return nil
	}

	*rrs = reply.Answer
	return nil
}

/*
 * Private - Docker agent
 */

func (d *DockerAgent) PostStart() error {
	// wait for Docker API to start
	// TODO: this works, but should replace with sd-notify
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
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pendingUIEntities[entity] = true
	d.uiEventDebounce.Call()
}

func (d *DockerAgent) triggerAllUIEvents() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i := range d.pendingUIEntities {
		d.pendingUIEntities[i] = true
	}
	d.uiEventDebounce.Call()
}

func (d *DockerAgent) doSendUIEvent() error {
	// can be triggered by UI before dockerd starts
	d.Running.Wait()

	d.mu.Lock()
	defer d.mu.Unlock()

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
		images, err := d.client.ListImagesFull()
		if err != nil {
			return err
		}
		event.CurrentImages = images
	}

	err := d.host.OnUIEvent(uitypes.UIEvent{
		Docker: &event,
	})
	if err != nil {
		return err
	}

	for i := range d.pendingUIEntities {
		d.pendingUIEntities[i] = false
	}
	return nil
}

func (d *DockerAgent) deleteK8sContainers() error {
	if d.k8s != nil {
		return nil
	}

	containers, err := d.client.ListContainers(true /*all*/)
	if err != nil {
		return err
	}

	// on start, delete k8s containers if we're not in k8s mode
	for _, c := range containers {
		if c.Labels != nil {
			if _, ok := c.Labels["io.kubernetes.pod.namespace"]; ok {
				// delete it
				err := d.client.Call("DELETE", "/containers/"+c.ID+"?force=true", nil, nil)
				if err != nil {
					logrus.WithError(err).WithField("cid", c.ID).Error("failed to delete k8s container")
				}
			}
		}
	}

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
	d.volumeRefreshDebounce.Call()
	d.imageRefreshDebounce.Call()
	// also kick all initial UI events for menu bar bg start
	d.triggerAllUIEvents()

	// delete k8s containers
	go func() {
		err := d.deleteK8sContainers()
		if err != nil {
			logrus.WithError(err).Error("failed to delete k8s containers")
		}
	}()

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
			default:
				// UI needs updating for health status, but nothing else
				// action format: "health_status: healthy"
				if strings.HasPrefix(event.Action, "health_status:") {
					d.triggerUIEvent(uitypes.DockerEntityContainer)
				}
			}

		case "volume":
			switch event.Action {
			// exclude "mount"/"unmount". UI's "in use" / "unused" is based on container's declared mounts, which will be handled by container events. it's not based on current *actively* mounted volumes
			case "create", "destroy":
				if event.Scope == "local" {
					d.triggerUIEvent(uitypes.DockerEntityVolume)
					d.volumeRefreshDebounce.Call()
				}
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
	a.docker.triggerAllUIEvents()
	return nil
}

func (a *AgentServer) DockerOnVmconfigUpdate(config *vmconfig.VmConfig, _ *None) error {
	return a.docker.OnVmconfigUpdate(config)
}

// mini freezer refcount tracker
// need this in order to act as a wakelock and guard UI event refreshes, and other important refreshes
// otherwise a freeze before refresh finishes = indefinite UI freeze
// TODO - this is not 100% perfect in terms of race
func (d *DockerAgent) incWakeRef() {
	// simply add and do nothing
	// if we're currently running then we will stay running until scon calls the predicate, at which point it'll read the atomic and return
	d.wakeRefs.Add(1)
}

func (d *DockerAgent) decWakeRef() {
	// remove...
	d.wakeRefs.Add(-1)
	// ... and re-trigger the predicate check to reconsider a freeze if scon's refcount is still 0
	err := d.scon.OnDockerRefsChanged()
	if err != nil {
		logrus.WithError(err).Error("failed to update scon refs")
	}
}
