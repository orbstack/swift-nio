package main

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type SconGuestServer struct {
	m               *ConManager
	dockerMachine   *Container
	vlanRouterIfi   int
	vlanMacTemplate net.HardwareAddr

	// only for OnDockerContainersChanged for now
	mu                    syncx.Mutex
	dockerContainersCache map[string]dockertypes.ContainerSummaryMin
}

func (s *SconGuestServer) Ping(_ None, _ *None) error {
	return nil
}

func containerToCfwdMeta(ctr *dockertypes.ContainerSummaryMin) bpf.CfwdContainerMeta {
	meta := bpf.CfwdContainerMeta{}
	if portStr, ok := ctr.Labels["dev.orbstack.http-port"]; ok {
		if port, err := strconv.ParseUint(portStr, 10, 16); err == nil {
			meta.HttpPort = uint16(port)
		}
	}
	if portStr, ok := ctr.Labels["dev.orbstack.https-port"]; ok {
		if port, err := strconv.ParseUint(portStr, 10, 16); err == nil {
			meta.HttpsPort = uint16(port)
		}
	}
	return meta
}

func (s *SconGuestServer) recvAndMountRootfsFdxLocked(ctr *dockertypes.ContainerSummaryMin, fdxSeq uint64) error {
	var fd int
	// locked by caller
	err := s.dockerMachine.useAgentLocked(func(a *agent.Client) error {
		var err error
		fd, err = a.Fdx().RecvFdInt(fdxSeq)
		return err
	})
	if err != nil {
		return fmt.Errorf("receive rootfs fd: %w", err)
	}
	defer unix.Close(fd)

	name := ctr.ID
	if len(ctr.Names) > 0 {
		name = strings.TrimPrefix(ctr.Names[0], "/")
	}

	// create dir in nfs containers
	// validate ID to prevent escape - this is untrusted data
	if strings.Contains(name, "/") {
		return fmt.Errorf("invalid container ID: %s", name)
	}

	// move mount
	err = s.m.nfsContainers.Mount("", name, "", 0, "", 0, 0, func(destPath string) error {
		err := unix.MoveMount(fd, "", unix.AT_FDCWD, destPath, unix.MOVE_MOUNT_F_EMPTY_PATH)
		if err != nil {
			return fmt.Errorf("move mount %s: %w", destPath, err)
		}

		// make rprivate to prevent our unmounts from propagating
		// otherwise it breaks kind, which uses systemd, which remounts all as shared
		err = unix.Mount("", destPath, "", unix.MS_REC|unix.MS_PRIVATE, "")
		if err != nil {
			return fmt.Errorf("remount %s: %w", destPath, err)
		}

		// this is a recursive mount (open_tree was called with AT_RECURSIVE)
		// now unmount undesired /proc, /dev, /sys recursively
		// too many files and not very useful
		// TODO: scan for all pseudo filesystems
		err = unix.Unmount(destPath+"/proc", unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) {
			// EINVAL = not mounted
			return fmt.Errorf("unmount %s/p: %w", destPath, err)
		}
		err = unix.Unmount(destPath+"/dev", unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) {
			// EINVAL = not mounted
			return fmt.Errorf("unmount %s/d: %w", destPath, err)
		}
		err = unix.Unmount(destPath+"/sys", unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) {
			// EINVAL = not mounted
			return fmt.Errorf("unmount %s/s: %w", destPath, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("move mount: %w", err)
	}

	// mount to export with new fsid
	err = s.m.nfsRoot.Mount("", "docker/containers/"+name, "", 0, "", 0, 0, func(destPath string) error {
		// TODO: shadow mount is NOT needed for /nfs/containers
		return s.m.fpll.StartMount(nfsDirContainers+"/ro/"+name, destPath)
	})
	if err != nil {
		return fmt.Errorf("bind mount: %w", err)
	}

	return nil
}

func (s *SconGuestServer) onDockerContainerRemovedFromCache(cid string) error {
	// needs mutex! called from both scon guest rpc and from runc wrap server
	s.mu.Lock()
	defer s.mu.Unlock()

	// look up in cache
	ctr, ok := s.dockerContainersCache[cid]
	if !ok {
		// not running, or not yet added to scon (due to debounce delay)
		return nil
	}

	logrus.WithField("cid", cid).Debug("removing container due to restart")
	return s.onDockerContainersChangedLocked(sgtypes.ContainersDiff{
		Diff: sgtypes.Diff[dockertypes.ContainerSummaryMin]{
			Added:   nil,
			Removed: []dockertypes.ContainerSummaryMin{ctr},
		},
		AddedRootfsFdxSeqs: nil,
	})
}

// note: this is for start/stop, not create/delete
func (s *SconGuestServer) OnDockerContainersChanged(diff sgtypes.ContainersDiff, _ *None) error {
	// needs mutex! called from both scon guest rpc and from runc wrap server
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.onDockerContainersChangedLocked(diff)
}

func (s *SconGuestServer) onDockerContainersChangedLocked(diff sgtypes.ContainersDiff) error {
	dockerBpf := s.dockerMachine.bpf // nil if not running anymore

	// IMPORTANT: must not return from this function before reading and closing fds from AddedRootfsFdxSeqs

	// update mDNS registry
	// must remove before adding in case of recreate with same IPs/domain
	for _, ctr := range diff.Removed {
		delete(s.dockerContainersCache, ctr.ID)
		s.m.net.mdnsRegistry.RemoveContainer(&ctr)

		if dockerBpf != nil {
			ctrIPs := containerToMdnsIPs(&ctr)
			for _, ctrIP := range ctrIPs {
				err := dockerBpf.CfwdRemoveContainerMeta(ctrIP)
				if err != nil {
					logrus.WithError(err).WithField("ip", ctrIP).Error("failed to remove container from cfwd")
				}
			}
		}

		// unmount from nfs (ignore error)
		prettyName := ctr.ID
		if len(ctr.Names) > 0 {
			prettyName = strings.TrimPrefix(ctr.Names[0], "/")
		}

		// detach fuse mount first to avoid user-facing errors (socket not connected)
		err := s.m.nfsRoot.Unmount("docker/containers/" + prettyName)
		if err != nil {
			logrus.WithError(err).WithField("cname", prettyName).Error("failed to unmount container")
		}

		// must flush exports immediately for nfsd to close fds
		err = s.m.nfsRoot.Flush()
		if err != nil {
			logrus.WithError(err).Error("failed to flush nfs")
		}

		// kill fuse server to release fds
		// note: we may enter this code path even if it was never mounted (i.e. too fast)
		err = s.m.fpll.StopMount(nfsDirRoot + "/ro/docker/containers/" + prettyName)
		if err != nil {
			logrus.WithError(err).WithField("cname", prettyName).Error("failed to stop fs server")
		}

		// finally unmount underlying overlayfs
		err = s.m.nfsContainers.Unmount(prettyName)
		if err != nil {
			logrus.WithError(err).WithField("cname", prettyName).Error("failed to unmount rootfs")
		}
	}
	for i, ctr := range diff.Added {
		s.dockerContainersCache[ctr.ID] = ctr
		ctrIPs := s.m.net.mdnsRegistry.AddContainer(&ctr)

		if dockerBpf != nil {
			meta := containerToCfwdMeta(&ctr)
			for _, ctrIP := range ctrIPs {
				err := dockerBpf.CfwdAddContainerMeta(ctrIP, meta)
				if err != nil {
					logrus.WithError(err).WithField("ip", ctrIP).Error("failed to add container to cfwd")
				}
			}

			// mount nfs in shadow dir
			// this is under bpf check because that checks whether the machine is running
			fdxSeq := diff.AddedRootfsFdxSeqs[i]
			if fdxSeq != 0 {
				err := s.recvAndMountRootfsFdxLocked(&ctr, fdxSeq)
				if err != nil {
					logrus.WithError(err).WithField("cid", ctr.ID).Error("failed to mount rootfs")
				}
			}
		}
	}

	// attach cfwd to container net namespaces
	if dockerBpf != nil {
		err := s.dockerMachine.UseMountNs(func() error {
			// faster than checking container inspect's SandboxKey
			entries, err := os.ReadDir("/run/docker/netns")
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					// does not exist until first container starts
					entries = nil
				} else {
					return err
				}
			}

			return dockerBpf.CfwdUpdateNetNamespaces(entries)
		})
		if err != nil {
			return fmt.Errorf("update cfwd: %w", err)
		}
	}

	// flush exports for newly mounted containers
	err := s.m.nfsRoot.Flush()
	if err != nil {
		logrus.WithError(err).Error("failed to flush nfs")
	}

	return nil
}

func (s *SconGuestServer) OnDockerImagesChanged(diff sgtypes.Diff[sgtypes.TaggedImage], _ *None) error {
	fs, err := securefs.NewFromPath(conf.C().DockerDataDir)
	if err != nil {
		return err
	}
	defer fs.Close()

	// unmount old ones
	for _, timg := range diff.Removed {
		err := s.m.nfsRoot.Unmount("docker/images/" + timg.Tag)
		if err != nil {
			logrus.WithError(err).Error("failed to unmount docker image")
		}
	}

	// mount new ones
	for _, timg := range diff.Added {
		// for root only, to avoid hundreds of mounts in machines
		// TODO: extra tags should be symlinks to be semantically correct
		err := s.m.nfsRoot.MountImage(timg.Image, timg.Tag, fs)
		if err != nil {
			logrus.WithError(err).Error("failed to mount docker image")
		}
	}

	err = s.m.nfsRoot.Flush()
	if err != nil {
		logrus.WithError(err).Error("failed to flush nfs")
	}

	return nil
}

func mountVolume(nfs NfsMirror, vol *dockertypes.Volume, fs *securefs.FS) error {
	// secure way: open the fd and bind it from O_PATH
	dir := strings.TrimPrefix(vol.Mountpoint, "/var/lib/docker")
	fd, err := fs.OpenFd(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open volume dir '%s': %w", dir, err)
	}
	defer unix.Close(fd)

	err = nfs.MountBind("/proc/self/fd/"+strconv.Itoa(fd), "docker/volumes/"+vol.Name, 0, 0)
	if err != nil {
		return fmt.Errorf("mount volume: %w", err)
	}

	return nil
}

func (s *SconGuestServer) OnDockerVolumesChanged(diff sgtypes.Diff[*dockertypes.Volume], _ *None) error {
	fs, err := securefs.NewFromPath(conf.C().DockerDataDir)
	if err != nil {
		return err
	}
	defer fs.Close()

	// unmount old ones
	for _, vol := range diff.Removed {
		// machines get volume mounts too, esp. because docker machine needs it for ppl mounting from mac nfs path
		err := s.m.nfsForAll.Unmount("docker/volumes/" + vol.Name)
		if err != nil {
			logrus.WithError(err).Error("failed to unmount docker volume")
		}
	}

	// mount new ones
	for _, vol := range diff.Added {
		err := mountVolume(s.m.nfsForAll, vol, fs)
		if err != nil {
			logrus.WithError(err).Error("failed to mount docker volume")
		}
	}

	err = s.m.nfsRoot.Flush()
	if err != nil {
		logrus.WithError(err).Error("failed to flush nfs")
	}

	return nil
}

func (s *SconGuestServer) OnDockerRefsChanged(_ None, _ *None) error {
	freezer := s.dockerMachine.Freezer()
	if freezer != nil {
		freezer.IncRef()
		freezer.DecRef()
	}

	return nil
}

func (s *SconGuestServer) clearDockerContainersCache() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.dockerContainersCache)
}

func ListenSconGuest(m *ConManager) error {
	dockerMachine, err := m.GetByID(ContainerIDDocker)
	if err != nil {
		return err
	}

	vlanRouterIf, err := net.InterfaceByName(ifVmnetDocker)
	if err != nil {
		return err
	}

	vlanMacTemplate, err := net.ParseMAC(netconf.VlanRouterMACTemplate)
	if err != nil {
		return err
	}

	server := &SconGuestServer{
		m:                     m,
		dockerMachine:         dockerMachine,
		vlanRouterIfi:         vlanRouterIf.Index,
		vlanMacTemplate:       vlanMacTemplate,
		dockerContainersCache: make(map[string]dockertypes.ContainerSummaryMin),
	}
	rpcServer := rpc.NewServer()
	err = rpcServer.RegisterName("scg", server)
	if err != nil {
		return err
	}

	// perms: root only (it's only for docker agent)
	listener, err := util.ListenUnixWithPerms(mounts.HostSconGuestSocket, 0600, 0, 0)
	if err != nil {
		return err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	runcWrap, err := NewRuncWrapServer(server)
	if err != nil {
		return err
	}
	go func() {
		err := runcWrap.Serve()
		if err != nil {
			logrus.WithError(err).Error("runc wrap server failed")
		}
	}()

	m.sconGuest = server
	return nil
}
