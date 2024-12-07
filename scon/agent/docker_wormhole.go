package agent

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const LabelWormholeType = "dev.orbstack.wormhole.type"
const LabelWormholeEphemeral = "dev.orbstack.wormhole.ephemeral"

var (
	ErrContainerNotRunning = errors.New("container is not running")

	errNoSuchImage = errors.New("no such image")
)

type StartWormholeArgs struct {
	Target string
}

type StartWormholeResponse struct {
	InitPid    int
	WorkingDir string
	Env        []string

	// if we created a container/image, return ID so caller can clean up
	State      WormholeSessionState
	SwitchRoot bool

	WarnImageWrite     bool
	WarnContainerWrite bool

	FdxSeq uint64
}

type StartWormholeResponseClient struct {
	StartWormholeResponse

	InitPidfdFile *os.File
	RootfsFile    *os.File
	FanotifyFile  *os.File
}

type EndWormholeArgs struct {
	State WormholeSessionState
}

type WormholeSessionState struct {
	CreatedContainerID string
	CreatedImageID     string
}

func randomContainerName() string {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, rand.Uint32())
	return fmt.Sprintf("orb-wormhole-temp-%s", hex.EncodeToString(buf))
}

func (d *DockerAgent) createWormholeImageContainer(imageID string) (string, error) {
	image, err := d.realClient.InspectImage(imageID)
	if err != nil {
		return "", err
	}

	// generate entrypoint script
	env := generateEntrypointEnv(image.Config)

	id, err := d.realClient.RunContainer(dockerclient.RunContainerOptions{
		Name: randomContainerName(),
	}, &dockertypes.ContainerConfig{
		Image: image.ID,
		Entrypoint: []string{
			"/dev/shm/.orb-wormhole-stub",
		},
		Env: env,
		Labels: map[string]string{
			LabelWormholeType:      "temp-image",
			LabelWormholeEphemeral: "1",
		},
		StopSignal: "SIGKILL",
		HostConfig: &dockertypes.ContainerHostConfig{
			AutoRemove: true,
			Binds: []string{
				mounts.WormholeStub + ":/dev/shm/.orb-wormhole-stub",
			},
		},
	})
	if err != nil {
		var apiErr *dockerclient.APIError
		if errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Message, "No such image:") {
			return "", errNoSuchImage
		}

		return "", err
	}

	return id, nil
}

const FAN_MOUNT_PERM = 0x00800000

func makeContainerStartFanotify(workDir string) (_fanFd int, retErr error) {
	fanFd, err := unix.FanotifyInit(unix.FAN_CLASS_PRE_CONTENT|unix.FAN_CLOEXEC|unix.FAN_UNLIMITED_MARKS|unix.FAN_NONBLOCK, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOATIME)
	if err != nil {
		return -1, fmt.Errorf("fanotify_init: %w", err)
	}
	defer func() {
		if retErr != nil {
			unix.Close(fanFd)
		}
	}()

	// monitor two nodes:
	// /work is used for deletion (which doesn't trigger /lower)
	// mount doesn't trigger /work; it's the chown that triggers it afterwards
	// err = unix.FanotifyMark(fanFd, unix.FAN_MARK_ADD|unix.FAN_MARK_ONLYDIR, unix.FAN_OPEN_PERM|unix.FAN_ACCESS_PERM|unix.FAN_ONDIR, unix.AT_FDCWD, workDir)
	// if err != nil {
	// 	return -1, fmt.Errorf("fanotify_mark: %w", err)
	// }

	// /lower is used for container start (overlay2 reads it first before mounting: https://github.com/moby/moby/blob/7b0ef10a9a28ac69c4fd89d82ae71f548b5c7edd/daemon/graphdriver/overlay2/overlay.go#L526)
	// /work is too late for the mounting case
	parentDir := filepath.Dir(workDir)
	mergedDir := parentDir + "/merged"
	_ = unix.Mkdir(mergedDir, 0o755)
	err = unix.FanotifyMark(fanFd, unix.FAN_MARK_ADD, FAN_MOUNT_PERM|unix.FAN_ONDIR, unix.AT_FDCWD, mergedDir)
	if err != nil {
		return -1, fmt.Errorf("fanotify_mark: %w", err)
	}

	return fanFd, nil
}

// TODO: port to old mount API (need v6.7+ to append lowerdirs)
func makeOverlayMount(lowerDir, upperDir, workDir string, readOnly bool) (retFd int, retErr error) {
	fsFd, err := unix.Fsopen("overlay", unix.FSOPEN_CLOEXEC)
	if err != nil {
		return 0, fmt.Errorf("fsopen: %w", err)
	}
	defer unix.Close(fsFd)

	err = unix.FsconfigSetString(fsFd, "source", "overlay")
	if err != nil {
		return 0, fmt.Errorf("set source: %w", err)
	}

	// set all in order
	// FSCONFIG_SET_STRING is limited to 256 bytes per value, so add dirs one by one
	for _, lower := range strings.Split(lowerDir, ":") {
		err = unix.FsconfigSetString(fsFd, "lowerdir+", lower)
		if err != nil {
			return 0, fmt.Errorf("set lowerdir: %w", err)
		}
	}

	err = unix.FsconfigSetString(fsFd, "upperdir", upperDir)
	if err != nil {
		return 0, fmt.Errorf("set upperdir: %w", err)
	}

	err = unix.FsconfigSetString(fsFd, "workdir", workDir)
	if err != nil {
		return 0, fmt.Errorf("set workdir: %w", err)
	}

	err = unix.FsconfigCreate(fsFd)
	if err != nil {
		return 0, fmt.Errorf("create overlay: %w", err)
	}

	var attrs int
	if readOnly {
		attrs |= unix.MOUNT_ATTR_RDONLY
	}
	mountFd, err := unix.Fsmount(fsFd, unix.FSMOUNT_CLOEXEC, attrs)
	if err != nil {
		return 0, fmt.Errorf("fsmount: %w", err)
	}

	return mountFd, nil
}

func (d *DockerAgent) maybeSetContainerMode(mode string) string {
	if strings.HasPrefix(mode, "container:") {
		netCID := strings.TrimPrefix(mode, "container:")
		if netCtr, err := d.realClient.InspectContainer(netCID); err == nil && netCtr.State.Running {
			return mode
		}
	}
	return ""
}

func parseStrSlice(strSlice any) []string {
	if slice, ok := strSlice.([]any); ok {
		result := []string{}
		for _, str := range slice {
			if str, ok := str.(string); ok {
				result = append(result, str)
			}
		}
		return result
	} else if str, ok := strSlice.(string); ok {
		return []string{"/bin/sh", "-c", str}
	}

	return nil
}

func generateEntrypointEnv(config *dockertypes.ContainerConfig) []string {
	// generate entrypoint script
	// entrypoint + cmd = argv for run/exec
	entrypointArgv := append(parseStrSlice(config.Entrypoint), parseStrSlice(config.Cmd)...)
	entrypointCmd := shellescape.QuoteCommand(entrypointArgv)
	if entrypointCmd != "" {
		entrypointCmd += ` "$@"`
	}
	env := config.Env
	env = append(env, "_ORB_WORMHOLE_ENTRYPOINT="+entrypointCmd)
	return env
}

func (d *DockerAgent) createWormholeStoppedContainer(ctr *dockertypes.ContainerJSON) (_containerID string, _imageID string, retErr error) {
	// no recursion!
	if _, ok := ctr.Config.Labels[LabelWormholeEphemeral]; ok {
		return "", "", fmt.Errorf("cannot create wormhole from ephemeral container")
	}

	// first, commit the container's FS changes to an image so that they show up
	// this is also used as the rootfs if graph driver != overlay2
	imageID, err := d.realClient.CommitContainer(ctr.ID)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if retErr != nil {
			err := d.realClient.RemoveImage(imageID, true)
			if err != nil {
				retErr = errors.Join(retErr, err)
			}
		}
	}()

	// generate entrypoint script
	env := generateEntrypointEnv(ctr.Config)

	newCfg := &dockertypes.ContainerConfig{
		// exact SHA256 of committed image
		Image: ctr.Image,

		// copy relevant config properties
		Domainname:      ctr.Config.Domainname,
		User:            ctr.Config.User,
		Env:             env,
		WorkingDir:      ctr.Config.WorkingDir,
		NetworkDisabled: ctr.Config.NetworkDisabled,
		OnBuild:         ctr.Config.OnBuild,

		// wormhole stub properties
		Entrypoint: []string{
			"/dev/shm/.orb-wormhole-stub",
		},
		Labels: map[string]string{
			LabelWormholeType:      "temp-container",
			LabelWormholeEphemeral: "1",
		},
		StopSignal: "SIGKILL",
		HostConfig: &dockertypes.ContainerHostConfig{
			// overrides
			AutoRemove: true,
			Binds:      []string{mounts.WormholeStub + ":/dev/shm/.orb-wormhole-stub"},

			// inherit Binds and Mounts (but add nocopy)
			// we don't copy VolumesFrom because the old container's MountPoints will also include its inherited MountPoints
			VolumesFrom: []string{ctr.ID},

			// need to be able to create /mnttmp if we'll be using raw overlayfs
			ReadonlyRootfs: ctr.HostConfig.ReadonlyRootfs && ctr.GraphDriver.Name != "overlay2",

			// copy relevant host config properties
			CpuShares:            ctr.HostConfig.CpuShares,
			Memory:               ctr.HostConfig.Memory,
			CgroupParent:         ctr.HostConfig.CgroupParent,
			BlkioWeight:          ctr.HostConfig.BlkioWeight,
			BlkioWeightDevice:    ctr.HostConfig.BlkioWeightDevice,
			BlkioDeviceReadBps:   ctr.HostConfig.BlkioDeviceReadBps,
			BlkioDeviceWriteBps:  ctr.HostConfig.BlkioDeviceWriteBps,
			BlkioDeviceReadIOps:  ctr.HostConfig.BlkioDeviceReadIOps,
			BlkioDeviceWriteIOps: ctr.HostConfig.BlkioDeviceWriteIOps,
			CpuPeriod:            ctr.HostConfig.CpuPeriod,
			CpuQuota:             ctr.HostConfig.CpuQuota,
			CpuRealtimePeriod:    ctr.HostConfig.CpuRealtimePeriod,
			CpuRealtimeRuntime:   ctr.HostConfig.CpuRealtimeRuntime,
			CpusetCpus:           ctr.HostConfig.CpusetCpus,
			CpusetMems:           ctr.HostConfig.CpusetMems,
			Devices:              ctr.HostConfig.Devices,
			DeviceCgroupRules:    ctr.HostConfig.DeviceCgroupRules,
			DeviceRequests:       ctr.HostConfig.DeviceRequests,
			KernelMemoryTCP:      ctr.HostConfig.KernelMemoryTCP,
			MemoryReservation:    ctr.HostConfig.MemoryReservation,
			MemorySwap:           ctr.HostConfig.MemorySwap,
			MemorySwappiness:     ctr.HostConfig.MemorySwappiness,
			NanoCpus:             ctr.HostConfig.NanoCpus,
			OomKillDisable:       ctr.HostConfig.OomKillDisable,
			PidsLimit:            ctr.HostConfig.PidsLimit,
			Ulimits:              ctr.HostConfig.Ulimits,
			CpuCount:             ctr.HostConfig.CpuCount,
			CpuPercent:           ctr.HostConfig.CpuPercent,
			IOMaximumIOps:        ctr.HostConfig.IOMaximumIOps,
			IOMaximumBandwidth:   ctr.HostConfig.IOMaximumBandwidth,
			VolumeDriver:         ctr.HostConfig.VolumeDriver,
			ConsoleSize:          ctr.HostConfig.ConsoleSize,
			CapAdd:               ctr.HostConfig.CapAdd,
			CapDrop:              ctr.HostConfig.CapDrop,
			Dns:                  ctr.HostConfig.Dns,
			DnsOptions:           ctr.HostConfig.DnsOptions,
			DnsSearch:            ctr.HostConfig.DnsSearch,
			ExtraHosts:           ctr.HostConfig.ExtraHosts,
			GroupAdd:             ctr.HostConfig.GroupAdd,
			Cgroup:               ctr.HostConfig.Cgroup,
			Links:                ctr.HostConfig.Links,
			OomScoreAdj:          ctr.HostConfig.OomScoreAdj,
			Privileged:           ctr.HostConfig.Privileged,
			PublishAllPorts:      ctr.HostConfig.PublishAllPorts,
			SecurityOpt:          ctr.HostConfig.SecurityOpt,
			StorageOpt:           ctr.HostConfig.StorageOpt,
			Tmpfs:                ctr.HostConfig.Tmpfs,
			ShmSize:              ctr.HostConfig.ShmSize,
			Sysctls:              ctr.HostConfig.Sysctls,
			Runtime:              ctr.HostConfig.Runtime,
			Isolation:            ctr.HostConfig.Isolation,
			MaskedPaths:          ctr.HostConfig.MaskedPaths,
			ReadonlyPaths:        ctr.HostConfig.ReadonlyPaths,

			// don't pollute logs in case engine has a fancy logging setup
			LogConfig: &dockertypes.ContainerLogConfig{
				Type: "none",
			},

			// not included: Init, ContainerIDFile, PortBindings (could conflict), RestartPolicy, AutoRemove, Annotations, Mounts (handled by VolumesFrom)
		},

		// not included: Volumes (handled by Volumes From), MacAddress (deprecated; moved to NetworkingConfig)

		// only copy NetworkingConfig from the original container
		// if target NetworkMode container is stopped, copying its config can result in confusing behavior (e.g. k8s pod sandbox has no networks, relying on k8s to configure them instead, and no networks = broken nix package installation)
		// it could also result in conflicts if users are trying to debug multiple containers attached to the same netns
		NetworkingConfig: &dockertypes.NetworkNetworkingConfig{
			EndpointsConfig: ctr.NetworkSettings.Networks,
		},
	}

	// copy orbstack domain port mappings
	if val, ok := ctr.Config.Labels["dev.orbstack.http-port"]; ok {
		newCfg.Labels["dev.orbstack.http-port"] = val
	}
	if val, ok := ctr.Config.Labels["dev.orbstack.https-port"]; ok {
		newCfg.Labels["dev.orbstack.https-port"] = val
	}

	// clear explicit IP assignments: can race and conflict because overlay2 start hook can run after network checks
	for _, n := range newCfg.NetworkingConfig.EndpointsConfig {
		n.IPAddress = ""
		n.GlobalIPv6Address = ""
		n.IPAMConfig = nil
	}

	// only copy NetworkMode if the netns source container is running
	// can't attach to netns of a stopped container
	newCfg.HostConfig.NetworkMode = d.maybeSetContainerMode(ctr.HostConfig.NetworkMode)
	// same applies to CgroupnsMode, IpcMode, PidMode, UTSMode, UsernsMode
	newCfg.HostConfig.CgroupnsMode = d.maybeSetContainerMode(ctr.HostConfig.CgroupnsMode)
	newCfg.HostConfig.IpcMode = d.maybeSetContainerMode(ctr.HostConfig.IpcMode)
	newCfg.HostConfig.PidMode = d.maybeSetContainerMode(ctr.HostConfig.PidMode)
	newCfg.HostConfig.UTSMode = d.maybeSetContainerMode(ctr.HostConfig.UTSMode)
	newCfg.HostConfig.UsernsMode = d.maybeSetContainerMode(ctr.HostConfig.UsernsMode)

	// Hostname is not allowed if NetworkMode is set to another container
	if !strings.HasPrefix(newCfg.HostConfig.NetworkMode, "container:") {
		newCfg.Hostname = ctr.Config.Hostname
	}

	// make a new container that copies most properties from the original container
	containerID, err := d.realClient.RunContainer(dockerclient.RunContainerOptions{
		Name: randomContainerName(),
	}, newCfg)
	if err != nil {
		return "", "", err
	}

	return containerID, imageID, nil
}

// prep: get container's init pid and open its rootfs dirfd
func (a *AgentServer) DockerStartWormhole(args *StartWormholeArgs, reply *StartWormholeResponse) (retErr error) {
	var initPid int
	var workingDir string
	var env []string
	var state WormholeSessionState
	rootfsFd := -1
	fanotifyFd := -1
	switchRoot := false
	warnImageWrite := false
	warnContainerWrite := false
	if conf.Debug() && args.Target == sshtypes.WormholeIDDocker {
		// debug only: wormhole for docker machine (ovm)
		initPid = 1
		workingDir = "/"
		env = []string{}
	} else {
		// standard path: for docker containers
		ctr, err := a.docker.realClient.InspectContainer(args.Target)
		if err != nil {
			if dockerclient.IsStatusError(err, http.StatusNotFound) {
				// container not found. try interpreting target as an image and creating a container from it
				state.CreatedContainerID, err = a.docker.createWormholeImageContainer(args.Target)
				if err != nil {
					if errors.Is(err, errNoSuchImage) {
						return fmt.Errorf("no such container or image: %s", args.Target)
					} else {
						return err
					}
				}

				ctr, err = a.docker.realClient.InspectContainer(state.CreatedContainerID)
				if err != nil {
					return err
				}

				warnImageWrite = true
			} else {
				return err
			}
		}

		// kata and gvisor runtimes won't work
		if strings.Contains(ctr.HostConfig.Runtime, "kata") || strings.Contains(ctr.HostConfig.Runtime, "gvisor") {
			return fmt.Errorf("unsupported container runtime: %s", ctr.HostConfig.Runtime)
		}

		if ctr.State.Pid == 0 {
			if state.CreatedContainerID != "" {
				return fmt.Errorf("newly-created container %s crashed", ctr.ID)
			}

			// not running. clone the container to allow debugging stopped containers
			state.CreatedContainerID, state.CreatedImageID, err = a.docker.createWormholeStoppedContainer(ctr)
			if err != nil {
				return err
			}
			defer func() {
				if retErr != nil {
					err := a.DockerEndWormhole(&EndWormholeArgs{State: state}, nil)
					if err != nil {
						retErr = errors.Join(retErr, err)
					}
				}
			}()

			// with overlay2 graph driver, we can use the real overlay dirs for live read and write
			if ctr.GraphDriver.Name == "overlay2" {
				// create overlay mount
				rootfsFd, err = makeOverlayMount(ctr.GraphDriver.Data["LowerDir"], ctr.GraphDriver.Data["UpperDir"], ctr.GraphDriver.Data["WorkDir"], ctr.HostConfig.ReadonlyRootfs)
				if err != nil {
					return fmt.Errorf("mount overlay: %w", err)
				}
				defer unix.Close(rootfsFd)
				switchRoot = true

				// TODO: fix tiny race by creating fanotify first. requires that we start monitoring immediately to avoid deadlock on overlay mount
				fanotifyFd, err = makeContainerStartFanotify(ctr.GraphDriver.Data["WorkDir"])
				if err != nil {
					return fmt.Errorf("make fanotify: %w", err)
				}
				defer unix.Close(fanotifyFd)
			} else {
				// TODO: fallback using diff + tar (for remote)
				warnContainerWrite = true
			}

			ctr, err = a.docker.realClient.InspectContainer(state.CreatedContainerID)
			if err != nil {
				return err
			}
		}

		initPid = ctr.State.Pid
		workingDir = ctr.Config.WorkingDir
		env = ctr.Config.Env
	}

	if initPid == 0 {
		return ErrContainerNotRunning
	}

	var err error
	if rootfsFd == -1 {
		rootfsFd, err = unix.Open(fmt.Sprintf("/proc/%d/root", initPid), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer unix.Close(rootfsFd)
	}

	initPidfd, err := unix.PidfdOpen(initPid, unix.PIDFD_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(initPidfd)

	fds := []int{initPidfd, rootfsFd}
	if fanotifyFd != -1 {
		fds = append(fds, fanotifyFd)
	}
	fdxSeq, err := a.fdx.SendFdsInt(fds...)
	if err != nil {
		return err
	}

	*reply = StartWormholeResponse{
		InitPid:    initPid,
		WorkingDir: workingDir,
		Env:        env,
		State:      state,
		SwitchRoot: switchRoot,
		FdxSeq:     fdxSeq,

		WarnImageWrite:     warnImageWrite,
		WarnContainerWrite: warnContainerWrite,
	}

	return nil
}

func (a *AgentServer) DockerEndWormhole(args *EndWormholeArgs, reply *None) error {
	var errs []error
	logrus.WithField("state", args.State).Debug("ending agent wormhole session")

	// delete container using auto-remove
	if args.State.CreatedContainerID != "" {
		err := a.docker.realClient.KillContainer(args.State.CreatedContainerID)
		if err != nil && !dockerclient.IsStatusError(err, http.StatusNotFound) && !strings.Contains(err.Error(), "is not running") {
			errs = append(errs, err)
		}
	}

	// delete image
	if args.State.CreatedImageID != "" {
		// if we need to delete an image, then synchronously wait for the container to exit, so that its reference is gone
		// we only need to wait for stopped state, not removed, because removing an image with force=true will also remove stopped containers
		err := a.docker.realClient.WaitContainer(args.State.CreatedContainerID)
		if err != nil && !dockerclient.IsStatusError(err, http.StatusNotFound) && !strings.Contains(err.Error(), "is not running") {
			errs = append(errs, err)
		}

		err = a.docker.realClient.RemoveImage(args.State.CreatedImageID, true)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
