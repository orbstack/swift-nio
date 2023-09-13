package main

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type SconGuestServer struct {
	m               *ConManager
	dockerMachine   *Container
	vlanRouterIfi   int
	vlanMacTemplate net.HardwareAddr
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

// note: this is for start/stop, not create/delete
func (s *SconGuestServer) OnDockerContainersChanged(diff sgtypes.Diff[dockertypes.ContainerSummaryMin], _ *None) error {
	// must not release lock - bpf is protected by c.mu
	s.dockerMachine.mu.RLock()
	defer s.dockerMachine.mu.RUnlock()
	dockerBpf := s.dockerMachine.bpf // nil if not running anymore

	// update mDNS registry
	// must remove before adding in case of recreate with same IPs/domain
	for _, ctr := range diff.Removed {
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
	}
	for _, ctr := range diff.Added {
		ctrIPs := s.m.net.mdnsRegistry.AddContainer(&ctr)

		if dockerBpf != nil {
			meta := containerToCfwdMeta(&ctr)
			for _, ctrIP := range ctrIPs {
				err := dockerBpf.CfwdAddContainerMeta(ctrIP, meta)
				if err != nil {
					logrus.WithError(err).WithField("ip", ctrIP).Error("failed to add container to cfwd")
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

			return s.dockerMachine.bpf.CfwdUpdateNetNamespaces(entries)
		})
		if err != nil {
			return fmt.Errorf("update cfwd: %w", err)
		}
	}

	return nil
}

func (s *SconGuestServer) OnDockerImagesChanged(diff sgtypes.Diff[sgtypes.TaggedImage], _ *None) error {
	fs, err := securefs.NewFS(conf.C().DockerDataDir)
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

	err = nfs.MountBind("/proc/self/fd/"+strconv.Itoa(fd), "docker/volumes/"+vol.Name)
	if err != nil {
		return fmt.Errorf("mount volume: %w", err)
	}

	return nil
}

func (s *SconGuestServer) OnDockerVolumesChanged(diff sgtypes.Diff[*dockertypes.Volume], _ *None) error {
	fs, err := securefs.NewFS(conf.C().DockerDataDir)
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

func ListenSconGuest(m *ConManager) error {
	dockerContainer, err := m.GetByID(ContainerIDDocker)
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
		m:               m,
		dockerMachine:   dockerContainer,
		vlanRouterIfi:   vlanRouterIf.Index,
		vlanMacTemplate: vlanMacTemplate,
	}
	rpcServer := rpc.NewServer()
	err = rpcServer.RegisterName("scg", server)
	if err != nil {
		return err
	}

	// perms: root only (it's only for docker agent)
	listener, err := util.ListenUnixWithPerms(mounts.SconGuestSocket, 0600, 0, 0)
	if err != nil {
		return err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return nil
}
