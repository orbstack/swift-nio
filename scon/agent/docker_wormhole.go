package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"golang.org/x/sys/unix"
)

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
	State WormholeSessionState

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

func (d *DockerAgent) createWormholeImageContainer(image string) (string, error) {
	id, err := d.client.RunContainer(&dockertypes.ContainerConfig{
		Image: image,
		Entrypoint: []string{
			"/dev/shm/.orb-wormhole-stub",
		},
		Labels: map[string]string{
			"dev.orbstack.wormhole.type": "temp-image",
		},
		StopSignal: "SIGKILL",
		HostConfig: &dockertypes.ContainerHostConfig{
			AutoRemove: true,
			Binds: []string{
				mounts.WormholeStub + ":/dev/shm/.orb-wormhole-stub",
			},
		},
	}, false)
	if err != nil {
		var apiErr *dockerclient.APIError
		if errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Message, "No such image:") {
			return "", errNoSuchImage
		}

		return "", err
	}

	return id, nil
}

func makeContainerStartFanotify(workDir string) (int, error) {
	dirFd, err := unix.Open(workDir, unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open workdir: %w", err)
	}
	defer unix.Close(dirFd)

	fanFd, err := unix.FanotifyInit(unix.FAN_CLASS_PRE_CONTENT|unix.FAN_CLOEXEC|unix.FAN_UNLIMITED_MARKS|unix.FAN_NONBLOCK, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOATIME)
	if err != nil {
		return -1, fmt.Errorf("fanotify_init: %w", err)
	}

	// TODO: FAN_DELETE? needs to be another fanotify mark
	err = unix.FanotifyMark(fanFd, unix.FAN_MARK_ADD|unix.FAN_MARK_ONLYDIR, unix.FAN_OPEN_PERM|unix.FAN_ONDIR, dirFd, "" /*nil*/)
	if err != nil {
		return -1, fmt.Errorf("fanotify_mark: %w", err)
	}

	return fanFd, nil
}

func (d *DockerAgent) createWormholeStoppedContainer(ctr *dockertypes.ContainerJSON) (string, string, error) {
	// first, commit the container's FS changes to an image so that they show up
	imageID, err := d.client.CommitContainer(ctr.ID)
	if err != nil {
		return "", "", err
	}

	// make a new container that copies most properties from the original container
	containerID, err := d.client.RunContainer(&dockertypes.ContainerConfig{
		// exact SHA256 of committed image
		Image: imageID,

		// copy relevant config properties
		Hostname:        ctr.Config.Hostname,
		Domainname:      ctr.Config.Domainname,
		User:            ctr.Config.User,
		Env:             ctr.Config.Env,
		WorkingDir:      ctr.Config.WorkingDir,
		NetworkDisabled: ctr.Config.NetworkDisabled,
		OnBuild:         ctr.Config.OnBuild,

		// wormhole stub properties
		Entrypoint: []string{
			"/dev/shm/.orb-wormhole-stub",
		},
		Labels: map[string]string{
			"dev.orbstack.wormhole.type": "temp-container",
		},
		StopSignal: "SIGKILL",
		HostConfig: &dockertypes.ContainerHostConfig{
			// overrides
			AutoRemove: true,
			Binds:      []string{mounts.WormholeStub + ":/dev/shm/.orb-wormhole-stub"},

			// inherit Binds and Mounts (but add nocopy)
			// we don't copy VolumesFrom because the old container's MountPoints will also include its inherited MountPoints
			VolumesFrom: []string{ctr.ID},

			// copy relevant host config properties
			// TODO: consider fallback if Net/IPC/*Mode fails due to dependent container being stopped too
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
			NetworkMode:          ctr.HostConfig.NetworkMode,
			VolumeDriver:         ctr.HostConfig.VolumeDriver,
			ConsoleSize:          ctr.HostConfig.ConsoleSize,
			CapAdd:               ctr.HostConfig.CapAdd,
			CapDrop:              ctr.HostConfig.CapDrop,
			CgroupnsMode:         ctr.HostConfig.CgroupnsMode,
			Dns:                  ctr.HostConfig.Dns,
			DnsOptions:           ctr.HostConfig.DnsOptions,
			DnsSearch:            ctr.HostConfig.DnsSearch,
			ExtraHosts:           ctr.HostConfig.ExtraHosts,
			GroupAdd:             ctr.HostConfig.GroupAdd,
			IpcMode:              ctr.HostConfig.IpcMode,
			Cgroup:               ctr.HostConfig.Cgroup,
			Links:                ctr.HostConfig.Links,
			OomScoreAdj:          ctr.HostConfig.OomScoreAdj,
			PidMode:              ctr.HostConfig.PidMode,
			Privileged:           ctr.HostConfig.Privileged,
			PublishAllPorts:      ctr.HostConfig.PublishAllPorts,
			ReadonlyRootfs:       ctr.HostConfig.ReadonlyRootfs,
			SecurityOpt:          ctr.HostConfig.SecurityOpt,
			StorageOpt:           ctr.HostConfig.StorageOpt,
			Tmpfs:                ctr.HostConfig.Tmpfs,
			UTSMode:              ctr.HostConfig.UTSMode,
			UsernsMode:           ctr.HostConfig.UsernsMode,
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

		// TODO: NetworkingConfig
	}, false)
	if err != nil {
		return "", "", err
	}

	return containerID, imageID, nil
}

// prep: get container's init pid and open its rootfs dirfd
func (a *AgentServer) DockerStartWormhole(args *StartWormholeArgs, reply *StartWormholeResponse) error {
	var initPid int
	var workingDir string
	var env []string
	var state WormholeSessionState
	rootfsFd := -1
	fanotifyFd := -1
	if conf.Debug() && args.Target == sshtypes.WormholeIDDocker {
		// debug only: wormhole for docker machine (ovm)
		initPid = 1
		workingDir = "/"
		env = []string{}
	} else {
		// standard path: for docker containers
		ctr, err := a.docker.client.InspectContainer(args.Target)
		if err != nil {
			var apiErr *dockerclient.APIError
			if errors.As(err, &apiErr) && apiErr.HTTPStatus == 404 {
				// container not found. try interpreting target as an image and creating a container from it
				state.CreatedContainerID, err = a.docker.createWormholeImageContainer(args.Target)
				if err != nil {
					if errors.Is(err, errNoSuchImage) {
						return fmt.Errorf("no such container or image: %s", args.Target)
					} else {
						return err
					}
				}

				ctr, err = a.docker.client.InspectContainer(state.CreatedContainerID)
				if err != nil {
					return err
				}
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

			fanotifyFd, err = makeContainerStartFanotify(ctr.GraphDriver.Data["WorkDir"])
			if err != nil {
				return fmt.Errorf("make fanotify: %w", err)
			}
			defer unix.Close(fanotifyFd)

			ctr, err = a.docker.client.InspectContainer(state.CreatedContainerID)
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

	initPidfd, err := unix.PidfdOpen(initPid, 0)
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
		FdxSeq:     fdxSeq,
	}

	return nil
}

func (a *AgentServer) DockerEndWormhole(args *EndWormholeArgs, reply *None) error {
	// delete container using auto-remove
	if args.State.CreatedContainerID != "" {
		err := a.docker.client.KillContainer(args.State.CreatedContainerID)
		if err != nil {
			return err
		}
	}

	// delete image
	if args.State.CreatedImageID != "" {
		err := a.docker.client.RemoveImage(args.State.CreatedImageID, true)
		if err != nil {
			return err
		}
	}

	return nil
}
