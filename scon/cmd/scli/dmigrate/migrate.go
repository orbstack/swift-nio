package dmigrate

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/util/slicesx"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockerconf"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/sirupsen/logrus"
)

const (
	migrationAgentImage = "ghcr.io/orbstack/dmigrate-agent:1"

	RemoteStopTimeout = 10 * time.Second

	maxUnusedContainerAge = 1 * 30 * 24 * time.Hour // 1 month

	minWorkers = 1
	maxWorkers = 5
)

var (
	ErrEntitiesFailed = errors.New("some data failed to migrate")
)

type Migrator struct {
	srcClient    *dockerclient.Client
	destClient   *dockerclient.Client
	rawSrcSocket string

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
	All bool

	IncludeContainers bool
	IncludeVolumes    bool
	IncludeImages     bool
	/* networks are implicit by containers */

	ForceIfExisting bool
}

type entitySpec struct {
	containerID string
	volumeName  string
	networkName string
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
	// use unversioned API client for source.
	// it could be old: 20.10=v1.41, 24=v1.43
	// best-effort to make it work. old engine rejects new client version
	srcClient, err := dockerclient.NewWithUnixSocket(fromSocket, &dockerclient.Options{
		Unversioned: true,
	})
	if err != nil {
		return nil, err
	}
	destClient, err := dockerclient.NewWithUnixSocket(toSocket, nil)
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

func (m *Migrator) SetRawSrcSocket(rawSrcSocket string) {
	m.rawSrcSocket = rawSrcSocket
}

func (m *Migrator) finishOneEntity(spec *entitySpec) {
	m.mu.Lock()
	m.finishedEntities++
	logrus.WithField("finished", spec).Debug("Finished entity")
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

type engineManifest struct {
	Images     []*dockertypes.ImageSummary
	Containers []*dockertypes.ContainerSummary
	Networks   []dockertypes.Network
	Volumes    []*dockertypes.Volume
}

func enumerateSource(client *dockerclient.Client) (*engineManifest, error) {
	images, err := client.ListImages()
	if err != nil {
		return nil, fmt.Errorf("get images: %w", err)
	}

	containers, err := client.ListContainers(true)
	if err != nil {
		return nil, fmt.Errorf("get containers: %w", err)
	}

	networks, err := client.ListNetworks()
	if err != nil {
		return nil, fmt.Errorf("get networks: %w", err)
	}

	volumes, err := client.ListVolumes()
	if err != nil {
		return nil, fmt.Errorf("get volumes: %w", err)
	}

	// workaround for docker desktop bug: fetch each volume individually to get correct "device" label for bind mount vols
	// otherwise we get a host_mnt path
	// docker volume create -o o=bind -o type=none -o device=$PWD dockerdev-elastic-volume
	newVolumes := make([]*dockertypes.Volume, 0, len(volumes))
	for _, v := range volumes {
		vol, err := client.GetVolume(v.Name)
		if err != nil {
			return nil, fmt.Errorf("get volume: %w", err)
		}
		newVolumes = append(newVolumes, vol)
	}
	volumes = newVolumes

	return &engineManifest{
		Images:     images,
		Containers: containers,
		Networks:   networks,
		Volumes:    volumes,
	}, nil
}

func (m *Migrator) checkDestEntities() error {
	manifest, err := enumerateSource(m.destClient)
	if err != nil {
		return fmt.Errorf("enumerate dest: %w", err)
	}

	if len(manifest.Images) > 0 {
		return errors.New("images")
	}

	if len(manifest.Containers) > 0 {
		return errors.New("containers")
	}

	if len(manifest.Volumes) > 0 {
		return errors.New("volumes")
	}

	// need to filter out defaults
	var filteredNetworks []dockertypes.Network
	for _, n := range manifest.Networks {
		if n.Scope != "local" || n.Driver != "bridge" {
			continue
		}
		// skip default bridge
		if n.Name == "bridge" {
			continue
		}
		filteredNetworks = append(filteredNetworks, n)
	}
	if len(filteredNetworks) > 0 {
		return errors.New("networks")
	}

	return nil
}

func (m *Migrator) MigrateAll(params MigrateParams) error {
	// grab everything
	logrus.Info("Gathering info")
	manifest, err := enumerateSource(m.srcClient)
	if err != nil {
		return fmt.Errorf("enumerate src: %w", err)
	}

	// early network filtering for dependency pruning
	// we do network deps by *NAME* b/c ID of default none/host/bridge can differ
	eligibleNetworkNames := []string{"bridge", "host", "none"}
	for _, n := range manifest.Networks {
		logrus.WithField("network", n.Name).Debug("Checking network")
		if n.Scope != "local" || n.Driver != "bridge" {
			logrus.WithField("network", n.Name).Debug("Skipping network: not local/bridge")
			continue
		}
		eligibleNetworkNames = append(eligibleNetworkNames, n.Name)
	}

	// FILTER CONTAINERS
	var filteredContainers []*dockertypes.ContainerSummary
	containerDeps := make(map[string][]entitySpec)
outer:
	for _, c := range manifest.Containers {
		logrus.WithField("container", c.Names).Debug("Checking container")
		// skip migration image ones (won't work b/c migration img is excluded)
		if c.Image == migrationAgentImage {
			logrus.WithField("container", c.Names).Debug("Skipping container: migration agent")
			continue
		}

		// skip containers not used for >1 month (need to fetch full info)
		if !params.All {
			fullCtr, err := m.srcClient.InspectContainer(c.ID)
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
				logrus.WithField("container", c.Names).Debug("Skipping container: old and unused")
				continue
			}

			// it's not possible to depend on a non-existent image or volume, but it *IS* possible to depend on a non-existent network. skip if so, otherwise migration gets stuck
			if c.NetworkSettings != nil && c.NetworkSettings.Networks != nil {
				for cnetName := range c.NetworkSettings.Networks {
					if !slices.Contains(eligibleNetworkNames, cnetName) {
						logrus.WithField("container", c.Names).Debug("Skipping container: depends on non-existent network")
						continue outer
					}
				}
			}

			// exclude k8s. without proper state it won't work
			if c.Labels != nil {
				if _, ok := c.Labels["io.kubernetes.pod.namespace"]; ok {
					logrus.WithField("container", c.Names).Debug("Skipping container: is kubernetes pod")
					continue
				}
			}
		}

		logrus.WithField("container", c.Names).Debug("Including container")
		filteredContainers = append(filteredContainers, c)
	}

	// FILTER NETWORKS: must be Scope="local" Driver="bridge" and referenced by container
	// 1. build map of container-referenced networks
	containerUsedNets := make(map[string]struct{})
	for _, c := range filteredContainers {
		if c.NetworkSettings == nil {
			logrus.WithField("container", c.Names).Debug("[build used map] Skipping container: no network settings")
			continue
		}
		if c.NetworkSettings.Networks == nil {
			logrus.WithField("container", c.Names).Debug("[build used map] Skipping container: no networks")
			continue
		}
		// don't trust the name, look through IDs
		for cnetName, cnet := range c.NetworkSettings.Networks {
			// if we won't be migrating it, then skip it as a dependency or we'll get deadlock
			if !slices.Contains(eligibleNetworkNames, cnetName) {
				logrus.WithField("container", c.Names).Debug("[build used map] Skipping container: depends on non-existent network")
				continue
			}
			if cnetName == "bridge" || cnetName == "host" || cnetName == "none" {
				logrus.WithField("container", c.Names).Debug("[build used map] Skipping container: depends on default network")
				continue
			}

			logrus.WithField("container", c.Names).WithField("network", cnetName).Debug("[build used map] Container uses network")
			containerUsedNets[cnet.NetworkID] = struct{}{}
			containerDeps[c.ID] = append(containerDeps[c.ID], entitySpec{networkName: cnetName})
		}
	}
	// 2. filter networks
	var filteredNetworks []dockertypes.Network
	for _, n := range manifest.Networks {
		logrus.WithField("network", n.Name).Debug("Checking network")
		if n.Scope != "local" || n.Driver != "bridge" {
			logrus.WithField("network", n.Name).Debug("Skipping network: not local/bridge")
			continue
		}
		// skip default networks
		if n.Name == "bridge" || n.Name == "host" || n.Name == "none" {
			logrus.WithField("network", n.Name).Debug("Skipping network: default network")
			continue
		}
		if !params.All {
			if _, ok := containerUsedNets[n.ID]; !ok {
				logrus.WithField("network", n.Name).Debug("Skipping network: not used by any containers")
				continue
			}
		}
		logrus.WithField("network", n.Name).Debug("Including network")
		filteredNetworks = append(filteredNetworks, n)
	}

	// FILTER VOLUMES: exclude anonymous volumes not referenced by any containers; local only
	// 1. build map of container-referenced volumes
	containerUsedVolumes := make(map[string][]*dockertypes.ContainerSummary)
	for _, c := range filteredContainers {
		if c.Mounts == nil {
			logrus.WithField("container", c.Names).Debug("[build used map] Skipping container: no mounts")
			continue
		}
		for _, m := range c.Mounts {
			// volume mounts only (skip dep if we're not migrating it)
			logrus.WithField("container", c.Names).WithField("mount", m.Name).Debug("[build used map] Checking mount")
			if m.Type != "volume" || m.Driver != "local" {
				logrus.WithField("container", c.Names).Debug("[build used map] Skipping container: non-local volume mount")
				continue
			}
			logrus.WithField("container", c.Names).WithField("mount", m.Name).Debug("[build used map] Container uses volume")
			containerUsedVolumes[m.Name] = append(containerUsedVolumes[m.Name], c)
			containerDeps[c.ID] = append(containerDeps[c.ID], entitySpec{volumeName: m.Name})
		}
	}
	// 2. filter volumes
	var filteredVolumes []*dockertypes.Volume
	for _, v := range manifest.Volumes {
		// TODO include non-local volumes
		logrus.WithField("volume", v.Name).Debug("Checking volume")
		if v.Driver != "local" || v.Scope != "local" {
			logrus.WithField("volume", v.Name).Debug("Skipping volume: not local")
			continue
		}
		if !params.All && v.Labels != nil {
			if _, ok := v.Labels["com.docker.volume.anonymous"]; ok {
				if _, ok := containerUsedVolumes[v.Name]; !ok {
					logrus.WithField("volume", v.Name).Debug("Skipping volume: anonymous and not used by any containers")
					continue
				}
			}
		}
		logrus.WithField("volume", v.Name).Debug("Including volume")
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
	var filteredImages []*dockertypes.ImageSummary
	for _, i := range manifest.Images {
		// exclude agent image
		if slices.Contains(i.RepoTags, migrationAgentImage) {
			logrus.WithField("image", i.ID).Debug("Skipping image: migration agent")
			continue
		}

		if _, ok := containerUsedImages[i.ID]; ok || params.All {
			logrus.WithField("image", i.ID).Debug("Including image: used by container")
			filteredImages = append(filteredImages, i)
			continue
		}

		// post-processing for API <1.43: remove '<none>:<none>' from RepoTags and '<none>@<none>' from RepoDigests
		i.RepoTags = slicesx.Filter(i.RepoTags, func(s string) bool {
			return s != "<none>:<none>"
		})
		i.RepoDigests = slicesx.Filter(i.RepoDigests, func(s string) bool {
			return s != "<none>@<none>"
		})

		// not referenced by a container
		// check if tagged and not pushed
		if len(i.RepoTags) > 0 && len(i.RepoDigests) == 0 {
			logrus.WithField("image", i.ID).Debug("Including image: tagged and not pushed")
			filteredImages = append(filteredImages, i)
			continue
		}
	}

	// check if dest has any entities
	if !params.ForceIfExisting {
		err = m.checkDestEntities()
		if err != nil {
			// bypass lint
			return fmt.Errorf("%s: %w. %s", "OrbStack's Docker engine already has data", err, "Use '-f' to force migration or 'orb delete docker' to clear existing data.")
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
	err = vmclient.Client().InternalSetDockerRemoteCtxAddr(m.rawSrcSocket) // skip proxy
	if err != nil {
		return fmt.Errorf("set docker remote ctx addr: %w", err)
	}
	defer vmclient.Client().InternalSetDockerRemoteCtxAddr("")

	// parallel workers
	workerCount := min(getPcpuCount()-1, maxWorkers)
	workerCount = max(workerCount, 1)
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
	logrus.Debug("Starting migration agent")
	srcAgentCid, err := m.srcClient.RunContainer(&dockertypes.ContainerCreateRequest{
		Image: migrationAgentImage,
		Cmd:   []string{"sleep", "1h"},
		HostConfig: &dockertypes.ContainerHostConfig{
			// perf: secccomp overhead
			Privileged: true,
			AutoRemove: true,
			// perf: net=host
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
	m.srcAgentCid = srcAgentCid
	defer func() {
		// [src] kill agent (+ auto-remove)
		logrus.Debug("Stopping migration agent")
		err := m.srcClient.KillContainer(srcAgentCid)
		if err != nil {
			logrus.Warnf("kill src container: %v", err)
		}
	}()

	// [src] check for free disk space for temp image saving
	srcStatfs, err := m.srcClient.Exec(srcAgentCid, &dockertypes.ContainerExecCreateRequest{
		Cmd:          []string{"stat", "-f", "-c", "%f %S", "/"},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("statfs: %w", err)
	}
	srcFsBlocks, srcFsBlockSize, ok := strings.Cut(strings.TrimSpace(srcStatfs), " ")
	if !ok {
		return fmt.Errorf("parse statfs: invalid output '%s'", srcStatfs)
	}
	srcFsBlocksInt, err := strconv.ParseInt(srcFsBlocks, 10, 64)
	if err != nil {
		return fmt.Errorf("parse statfs: %w", err)
	}
	srcFsBlockSizeInt, err := strconv.ParseInt(srcFsBlockSize, 10, 64)
	if err != nil {
		return fmt.Errorf("parse statfs: %w", err)
	}
	srcFsBytes := srcFsBlocksInt * srcFsBlockSizeInt
	var maxImageSize int64
	for _, i := range filteredImages {
		if i.Size > maxImageSize {
			maxImageSize = i.Size
		}
	}
	// min = 2x max image size, due to parallelism
	minFreeBytes := 2 * maxImageSize
	if srcFsBytes < minFreeBytes {
		return fmt.Errorf("%s: %d GB required. %s", "Not enough free disk space in Docker Desktop", minFreeBytes/(1000*1000*1000), "Please free up space or increase virtual disk size in Docker Desktop settings.")
	}

	// [dest] start sync server
	err = m.startSyncServer()
	if err != nil {
		return fmt.Errorf("start sync server: %w", err)
	}
	defer scli.Client().InternalDockerMigrationStopSyncServer()

	// all ready. migrate in order
	logrus.WithField("started", true).Info("Migration started")
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

	// 4. containers
	// depends on all above, so use a barrier
	// TODO restore dependency graph
	group.Wait()
	for _, c := range filteredContainers {
		c := c
		err = m.submitOneContainer(group, c)
		if err != nil {
			return err
		}
	}

	// end
	pool.StopAndWait()

	// get correct shell PATH to look up cred helpers, before migrating
	envPATH, err := vmclient.Client().InternalGetEnvPATH()
	if err != nil {
		return fmt.Errorf("get env PATH: %w", err)
	}
	err = os.Setenv("PATH", envPATH)
	if err != nil {
		return fmt.Errorf("set env PATH: %w", err)
	}

	// migrate docker credentials
	err = m.migrateCredentials()
	if err != nil {
		return fmt.Errorf("migrate credentials: %w", err)
	}

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
