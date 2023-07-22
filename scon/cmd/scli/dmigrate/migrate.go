package dmigrate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockerconf"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

const (
	migrationAgentImage = "ghcr.io/orbstack/dmigrate-agent:1"

	maxUnusedContainerAge = 6 * 30 * 24 * time.Hour // 6 months

	minWorkers = 1
	maxWorkers = 5
)

var (
	ErrEntitiesFailed = errors.New("some entities failed to migrate")
)

type Migrator struct {
	srcClient  *dockerclient.Client
	destClient *dockerclient.Client

	mu           sync.Mutex
	networkIDMap map[string]string

	ctrPauseRefsMu sync.Mutex
	ctrPauseRefs   map[string]int
	entityFinishCh chan struct{}

	srcAgentCid string
	syncPort    int

	finishedEntities int
	finishedDeps     []entitySpec // regardless of whether they succeeded
	totalEntities    int
}

type MigrateParams struct {
	IncludeContainers bool
	IncludeVolumes    bool
	IncludeImages     bool
	/* networks are implicit by containers */
}

type entitySpec struct {
	containerID string
	volumeName  string
	networkID   string
	imageID     string
}

type errorTracker struct {
	mu     sync.Mutex
	errors []error
}

func (e *errorTracker) Check() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return errors.Join(e.errors...)
}

func NewMigratorWithUnixSockets(fromSocket, toSocket string) (*Migrator, error) {
	srcClient, err := dockerclient.NewWithUnixSocket(fromSocket)
	if err != nil {
		return nil, err
	}
	destClient, err := dockerclient.NewWithUnixSocket(toSocket)
	if err != nil {
		return nil, err
	}

	return NewMigratorWithClients(srcClient, destClient)
}

func NewMigratorWithClients(srcClient, destClient *dockerclient.Client) (*Migrator, error) {
	return &Migrator{
		srcClient:  srcClient,
		destClient: destClient,

		networkIDMap:   make(map[string]string),
		ctrPauseRefs:   make(map[string]int),
		entityFinishCh: make(chan struct{}, 1),
	}, nil
}

func (m *Migrator) Close() {
	m.srcClient.Close()
	m.destClient.Close()
}

func findFreeTCPPort() (int, error) {
	// zero-port listener
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	// get port
	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

func splitRepoTag(repoTag string) (string, string) {
	// last index, to deal with "localhost:5000/myimage:latest"
	sepPos := strings.LastIndex(repoTag, ":")
	if sepPos == -1 {
		return repoTag, "latest"
	}

	repoPart := repoTag[:sepPos]
	tagPart := repoTag[sepPos+1:]
	return repoPart, tagPart
}

func (m *Migrator) createAndStartContainer(client *dockerclient.Client, req *dockertypes.ContainerCreateRequest) (string, error) {
	// need to pull image first
	repoPart, tagPart := splitRepoTag(req.Image)
	err := client.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
	if err != nil {
		return "", fmt.Errorf("pull image: %w", err)
	}

	// create --rm container
	var containerResp dockertypes.ContainerCreateResponse
	err = client.Call("POST", "/containers/create", req, &containerResp)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	// start container
	err = client.Call("POST", "/containers/"+containerResp.ID+"/start", nil, nil)
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	return containerResp.ID, nil
}

func demuxOutput(r io.Reader, w io.Writer) error {
	// decode multiplexed
	for {
		hdr := make([]byte, 8)
		_, err := r.Read(hdr)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			} else {
				return fmt.Errorf("read header: %w", err)
			}
		}
		// big endian uint32 from last 4 bytes
		size := binary.BigEndian.Uint32(hdr[4:8])
		// read that amount
		buf := make([]byte, size)
		n := 0
		for n < int(size) {
			nr, err := r.Read(buf[n:])
			if err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				} else {
					return fmt.Errorf("read body: %w", err)
				}
			}
			n += nr
		}
		// write out
		w.Write(buf)
	}
}

func (m *Migrator) finishOneEntity(spec *entitySpec) {
	m.mu.Lock()
	m.finishedEntities++
	if spec != nil {
		// works as adaptive notifications w/ non-blocking send + 1 buf
		m.finishedDeps = append(m.finishedDeps, *spec)

		select {
		case m.entityFinishCh <- struct{}{}:
		default:
		}
	}
	m.mu.Unlock()

	progress := float64(m.finishedEntities) / float64(m.totalEntities)
	m.sendProgressEvent(progress)
}

func (m *Migrator) sendProgressEvent(progress float64) {
	logrus.WithField("progress", progress*100).Info("")
}

func (m *Migrator) startSyncServer() error {
	syncPort, err := findFreeTCPPort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	m.syncPort = syncPort

	// start server
	err = scli.Client().InternalDockerMigrationRunSyncServer(types.InternalDockerMigrationRunSyncServerRequest{
		Port: syncPort,
	})
	if err != nil {
		return fmt.Errorf("start sync server: %w", err)
	}

	// wait for mac proxy to start. server will always be running
	pollTicker := time.NewTicker(serverPollInterval)
	timeout := time.NewTimer(serverStartTimeout)
loop:
	for {
		select {
		case <-pollTicker.C:
			// check if server is running
			conn, err := netx.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", syncPort))
			if err != nil {
				continue
			}

			// connected = fwd running
			conn.Close()
			err = nil
			break loop
		case <-timeout.C:
			// timeout
			err = fmt.Errorf("start server: timeout")
			break loop
		}
	}
	pollTicker.Stop()
	timeout.Stop()
	if err != nil {
		return err
	}

	return nil
}

func (m *Migrator) MigrateAll(params MigrateParams) error {
	// grab everything
	logrus.Info("Gathering info")
	var images []dockertypes.Image
	err := m.srcClient.Call("GET", "/images/json", nil, &images)
	if err != nil {
		return fmt.Errorf("get images: %w", err)
	}
	var containers []*dockertypes.ContainerSummary
	err = m.srcClient.Call("GET", "/containers/json?all=true", nil, &containers)
	if err != nil {
		return fmt.Errorf("get containers: %w", err)
	}
	var networks []dockertypes.Network
	err = m.srcClient.Call("GET", "/networks", nil, &networks)
	if err != nil {
		return fmt.Errorf("get networks: %w", err)
	}
	var volumesResp dockertypes.VolumeListResponse
	err = m.srcClient.Call("GET", "/volumes", nil, &volumesResp)
	if err != nil {
		return fmt.Errorf("get volumes: %w", err)
	}
	volumes := volumesResp.Volumes

	// FILTER CONTAINERS
	var filteredContainers []*dockertypes.ContainerSummary
	containerDeps := make(map[string][]entitySpec)
	for _, c := range containers {
		// skip migration image ones (won't work b/c migration img is excluded)
		if c.Image == migrationAgentImage {
			continue
		}

		// skip naturally exited containers, unless they're in a compose group
		if _, ok := c.Labels["com.docker.compose.project"]; !ok {
			if c.State == "exited" {
				continue
			}
		}

		// skip containers not used for >6 months (need to fetch full info)
		var fullCtr dockertypes.ContainerJSON
		err := m.srcClient.Call("GET", "/containers/"+c.ID+"/json", nil, &fullCtr)
		if err != nil {
			return fmt.Errorf("get src container: %w", err)
		}

		startedAt, err := time.Parse(time.RFC3339Nano, fullCtr.State.StartedAt)
		if err != nil {
			return fmt.Errorf("parse startedAt: %w", err)
		}
		finishedAt, err := time.Parse(time.RFC3339Nano, fullCtr.State.FinishedAt)
		if err != nil {
			return fmt.Errorf("parse finishedAt: %w", err)
		}
		if time.Since(startedAt) > maxUnusedContainerAge && time.Since(finishedAt) > maxUnusedContainerAge {
			logrus.WithField("container", c.ID).Debug("Skipping container: old and unused")
			continue
		}

		filteredContainers = append(filteredContainers, c)
	}

	// FILTER NETWORKS: must be Scope="local" Driver="bridge" and referenced by container
	// 1. build map of container-referenced networks
	containerUsedNets := make(map[string]struct{})
	for _, c := range filteredContainers {
		if c.NetworkSettings == nil {
			continue
		}
		if c.NetworkSettings.Networks == nil {
			continue
		}
		// don't trust the name, look through IDs
		for _, cnet := range c.NetworkSettings.Networks {
			containerUsedNets[cnet.NetworkID] = struct{}{}
			containerDeps[c.ID] = append(containerDeps[c.ID], entitySpec{networkID: cnet.NetworkID})
		}
	}
	// 2. filter networks
	var filteredNetworks []dockertypes.Network
	for _, n := range networks {
		if n.Scope != "local" || n.Driver != "bridge" {
			continue
		}
		// skip default bridge
		if n.Name == "bridge" {
			continue
		}
		if _, ok := containerUsedNets[n.ID]; !ok {
			continue
		}
		filteredNetworks = append(filteredNetworks, n)
	}

	// FILTER VOLUMES: exclude anonymous volumes not referenced by any containers; local only
	// 1. build map of container-referenced volumes
	containerUsedVolumes := make(map[string][]*dockertypes.ContainerSummary)
	for _, c := range filteredContainers {
		if c.Mounts == nil {
			continue
		}
		for _, m := range c.Mounts {
			// volume mounts only
			if m.Type != "volume" || m.Driver != "local" {
				continue
			}
			containerUsedVolumes[m.Name] = append(containerUsedVolumes[m.Name], c)
			containerDeps[c.ID] = append(containerDeps[c.ID], entitySpec{volumeName: m.Name})
		}
	}
	// 2. filter volumes
	var filteredVolumes []dockertypes.Volume
	for _, v := range volumes {
		// TODO include non-local volumes
		if v.Driver != "local" || v.Scope != "local" {
			continue
		}
		if v.Labels != nil {
			if _, ok := v.Labels["com.docker.volume.anonymous"]; ok {
				if _, ok := containerUsedVolumes[v.Name]; !ok {
					continue
				}
			}
		}
		filteredVolumes = append(filteredVolumes, v)
	}

	// FILTER IMAGES: either referenced by a container, OR (tagged AND not-pushed)
	// 1. build map of container-referenced images
	containerUsedImages := make(map[string]struct{})
	for _, c := range filteredContainers {
		containerUsedImages[c.ImageID] = struct{}{}
		containerDeps[c.ID] = append(containerDeps[c.ID], entitySpec{imageID: c.ImageID})
	}
	// 2. filter images
	var filteredImages []dockertypes.Image
	for _, i := range images {
		// exclude agent image
		if slices.Contains(i.RepoTags, migrationAgentImage) {
			continue
		}

		if _, ok := containerUsedImages[i.ID]; ok {
			filteredImages = append(filteredImages, i)
			continue
		}

		// not referenced by a container
		// check if tagged and not pushed
		if len(i.RepoTags) > 0 && len(i.RepoDigests) == 0 {
			filteredImages = append(filteredImages, i)
			continue
		}
	}

	// prep for progress
	m.finishedEntities = 0
	m.totalEntities = /*daemon config*/ 1 + len(filteredImages) + len(filteredVolumes) + len(filteredNetworks) + len(filteredContainers)

	// docker daemon config first
	err = m.migrateDaemonConfig(dockerconf.DockerDesktopDaemonConfig())
	if err != nil {
		return fmt.Errorf("migrate daemon config: %w", err)
	}

	// start docker remote ctx socket proxy
	err = vmclient.Client().InternalSetDockerRemoteCtxAddr(conf.DockerRemoteCtxSocket())
	if err != nil {
		return fmt.Errorf("set docker remote ctx addr: %w", err)
	}
	defer vmclient.Client().InternalSetDockerRemoteCtxAddr("")

	// parallel workers
	workerCount := getPcpuCount() - 1
	if workerCount < minWorkers {
		workerCount = minWorkers
	}
	if workerCount > maxWorkers {
		workerCount = maxWorkers
	}
	errTracker := &errorTracker{}
	pool := pond.New(workerCount, 100, pond.PanicHandler(func(p any) {
		var err error
		if e, ok := p.(error); ok {
			err = e
		} else {
			err = fmt.Errorf("panic: %v", p)
		}
		logrus.Error(err)

		errTracker.mu.Lock()
		errTracker.errors = append(errTracker.errors, err)
		errTracker.mu.Unlock()
	}))
	defer pool.StopAndWait()
	group := pool.Group()

	// [src] start agent
	srcAgentCid, err := m.createAndStartContainer(m.srcClient, &dockertypes.ContainerCreateRequest{
		Image: migrationAgentImage,
		Cmd:   []string{"sleep", "inf"},
		HostConfig: &dockertypes.ContainerHostConfig{
			// perf: secccomp overhead
			Privileged: true,
			AutoRemove: true,
			// perf: net=host
			NetworkMode: "host",
			Binds: []string{
				"/var/lib/docker:/var/lib/docker:rshared",
				"/var/run/docker.sock:/var/run/docker.sock",
				// anonymous volume for image packing
				"/tmp",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("run src agent: %w", err)
	}
	m.srcAgentCid = srcAgentCid
	defer func() {
		// [src] kill agent
		err := m.srcClient.Call("POST", "/containers/"+srcAgentCid+"/kill", nil, nil)
		if err != nil {
			logrus.Warnf("kill src container: %v", err)
		}
	}()

	// [dest] start sync server
	err = m.startSyncServer()
	if err != nil {
		return fmt.Errorf("start sync server: %w", err)
	}
	defer scli.Client().InternalDockerMigrationStopSyncServer()

	// all ready. migrate in order
	go http.ListenAndServe("localhost:6061", nil)

	// 1. images
	err = m.submitImages(group, filteredImages)
	if err != nil {
		return err
	}

	// 2. volumes
	err = m.submitVolumes(group, filteredVolumes, containerUsedVolumes)
	if err != nil {
		return err
	}

	// 3. networks
	err = m.submitNetworks(group, filteredNetworks)
	if err != nil {
		return err
	}

	// TODO plugins?

	// 4. containers (depends on all above)
	remainingContainers := filteredContainers
	for range m.entityFinishCh {
		// try to submit more containers
		var deferredContainers []*dockertypes.ContainerSummary // didn't make it into this round
		for _, c := range remainingContainers {
			// check if all deps are satisfied
			satisfied := true
			m.mu.Lock()
			for _, dep := range containerDeps[c.ID] {
				if !slices.Contains(m.finishedDeps, dep) {
					satisfied = false
					break
				}
			}
			m.mu.Unlock()

			if satisfied {
				// satisfied, submit
				err = m.submitOneContainer(group, c)
				if err != nil {
					return err
				}
			} else {
				deferredContainers = append(deferredContainers, c)
			}
		}

		// update remaining
		remainingContainers = deferredContainers

		// stop when everything has been submitted and all entities have finished
		if len(remainingContainers) == 0 && m.finishedEntities == m.totalEntities {
			break
		}
	}

	// end
	pool.StopAndWait()

	// restore ~/.docker/config.json credStore again if changed by starting Docker Desktop
	err = dockerconf.FixDockerCredsStore()
	if err != nil {
		return fmt.Errorf("fix docker creds store: %w", err)
	}

	// restore Docker context if changed
	// this uses same DOCKER_CONFIG and docker exe as vmgr
	err = vmclient.Client().SetDockerContext()
	if err != nil {
		return fmt.Errorf("set docker context: %w", err)
	}

	// dispatch any earlier errors
	err = errTracker.Check()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEntitiesFailed, err)
	}

	// deferred: [src] kill agent

	return nil
}
