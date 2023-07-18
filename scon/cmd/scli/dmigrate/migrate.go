package dmigrate

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockerconf"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

const (
	//migrationAgentImage = "ghcr.io/orbstack/dmigrate-agent:1"
	migrationAgentImage = "alpine:20230329"

	maxUnusedContainerAge = 6 * 30 * 24 * time.Hour // 6 months

	minWorkers = 1
	maxWorkers = 5
)

type Migrator struct {
	srcClient  *dockerclient.Client
	destClient *dockerclient.Client

	mu           sync.Mutex
	networkIDMap map[string]string
	srcAgentCid  string
}

type MigrateParams struct {
	IncludeContainers bool
	IncludeVolumes    bool
	IncludeImages     bool
	/* networks are implicit by containers */
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

	return &Migrator{
		srcClient:  srcClient,
		destClient: destClient,

		networkIDMap: make(map[string]string),
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

func (m *Migrator) MigrateAll(params MigrateParams) error {
	// grab everything
	var images []dockertypes.Image
	err := m.srcClient.Call("GET", "/images/json", nil, &images)
	if err != nil {
		return fmt.Errorf("get images: %w", err)
	}
	var containers []dockertypes.ContainerSummary
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
	var filteredContainers []dockertypes.ContainerSummary
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
		}
	}
	// 2. filter networks
	var filteredNetworks []dockertypes.Network
	for _, n := range networks {
		if n.Scope != "local" || n.Driver != "bridge" {
			continue
		}
		if _, ok := containerUsedNets[n.ID]; !ok {
			continue
		}
		filteredNetworks = append(filteredNetworks, n)
	}

	// FILTER VOLUMES: exclude anonymous volumes not referenced by any containers; local only
	// 1. build map of container-referenced volumes
	containerUsedVolumes := make(map[string]struct{})
	for _, c := range filteredContainers {
		if c.Mounts == nil {
			continue
		}
		for _, m := range c.Mounts {
			// volume mounts only
			if m.Type != "volume" || m.Driver != "local" {
				continue
			}
			containerUsedVolumes[m.Name] = struct{}{}
		}
	}
	// 2. filter volumes
	var filteredVolumes []dockertypes.Volume
	for _, v := range volumes {
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

	// docker daemon config first
	err = m.migrateDaemonConfig(dockerconf.DockerDesktopDaemonConfig())
	if err != nil {
		return fmt.Errorf("migrate daemon config: %w", err)
	}

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
	preContainerGroup := pool.Group()

	// [src] start agent
	srcAgentCid, err := m.createAndStartContainer(m.srcClient, &dockertypes.ContainerCreateRequest{
		Image: migrationAgentImage,
		Cmd:   []string{"sleep", "inf"},
		HostConfig: &dockertypes.ContainerHostConfig{
			Privileged: true,
			AutoRemove: true,
			// net=host for perf
			NetworkMode: "host",
			Binds: []string{
				"/var/lib/docker:/var/lib/docker:rshared",
				"/var/run/docker.sock:/var/run/docker.sock",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("run src agent: %w", err)
	}
	m.mu.Lock()
	m.srcAgentCid = srcAgentCid
	m.mu.Unlock()
	defer func() {
		// [src] kill agent
		err := m.srcClient.Call("POST", "/containers/"+srcAgentCid+"/kill", nil, nil)
		if err != nil {
			logrus.Warnf("kill src container: %v", err)
		}
	}()

	// TODO remove this once we build custom image
	_, err = execAs(m.srcClient, srcAgentCid, &dockertypes.ContainerExecCreateRequest{
		Cmd:          []string{"apk", "add", "tar", "socat"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("install deps: %w", err)
	}

	// all ready. migrate in order

	// 1. images
	err = m.submitImages(preContainerGroup, filteredImages)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// 2. volumes
	err = m.submitVolumes(preContainerGroup, filteredVolumes)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// 3. networks
	err = m.submitNetworks(preContainerGroup, filteredNetworks)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// wait for container deps
	preContainerGroup.Wait()
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// 4. containers (depends on all above)
	err = m.submitContainers(preContainerGroup, filteredContainers)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// end
	pool.StopAndWait()
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// restore ~/.docker/config.json credStore again if changed by starting Docker Desktop
	err = dockerconf.FixDockerCredsStore()
	if err != nil {
		return fmt.Errorf("fix docker creds store: %w", err)
	}

	// deferred: [src] kill agent

	return nil
}
