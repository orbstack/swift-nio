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
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
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

func (s *SconGuestServer) DockerAddBridge(config sgtypes.DockerBridgeConfig, _ *None) (retErr error) {
	// assign vlan ID, create vmnet bridge on host, add to VlanRouter
	vlanId, err := s.m.host.AddDockerBridge(config)
	if err != nil {
		return err
	}
	// cleanup host bridge on failure
	defer func() {
		if retErr != nil {
			_, err := s.m.host.RemoveDockerBridge(config)
			if err != nil {
				logrus.WithError(err).Error("failed to clean up host bridge after failed add")
			}
		}
	}()

	// copy MAC addr template
	// last byte: lower 7 bits are vlan ID, upper 1 bit is 1 (guest)
	// host
	hostMac := make(net.HardwareAddr, len(s.vlanMacTemplate))
	copy(hostMac, s.vlanMacTemplate)
	hostMac[5] = byte(vlanId & 0x7f)

	// open nsfd
	initPidF := s.dockerMachine.initPidFile
	if initPidF == nil {
		return fmt.Errorf("docker machine has no init pid")
	}

	// create macvlan
	// MAC of the macvlan interface doesn't matter because it's just a bridge member
	// real Linux packets come from container and the bridge master interface
	la := netlink.NewLinkAttrs()
	la.Name = fmt.Sprintf("%s%d", agent.DockerBridgeMirrorPrefix, vlanId)
	la.ParentIndex = s.vlanRouterIfi // parent = eth2
	la.MTU = 1500                    // doesn't really matter because GSO
	// move to container netns (doesn't accept pidfd as nsfd)
	la.Namespace = netlink.NsPid(s.dockerMachine.initPid)
	macvlan := &netlink.Macvlan{
		LinkAttrs: la,
		// filter by source MAC
		Mode: netlink.MACVLAN_MODE_SOURCE,
	}
	err = netlink.LinkAdd(macvlan)
	if err != nil {
		return fmt.Errorf("create mirror link: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = netlink.LinkDel(macvlan)
		}
	}()

	// now enter the container's netns... (interface is in there)
	_, err = sysnet.WithNetns(initPidF, func() (_ struct{}, retErr2 error) {
		defer func() {
			if retErr2 != nil {
				_ = netlink.LinkDel(macvlan)
			}
		}()

		err = netlink.LinkSetAllmulticastOff(macvlan)
		if err != nil {
			return struct{}{}, fmt.Errorf("set mirror link allmulticast off: %w", err)
		}

		// add host MAC to filter
		err = netlink.MacvlanMACAddrAdd(macvlan, hostMac)
		if err != nil {
			return struct{}{}, fmt.Errorf("add host mac to filter: %w", err)
		}

		// attach macvlan to docker bridge
		err = netlink.LinkSetMaster(macvlan, &netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: config.GuestInterfaceName,
			},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("attach mirror link: %w", err)
		}

		// set up
		err = netlink.LinkSetUp(macvlan)
		if err != nil {
			return struct{}{}, fmt.Errorf("set mirror link up: %w", err)
		}

		return struct{}{}, nil
	})
	if err != nil {
		return err
	}

	// add host ip to cfwd bpf
	s.dockerMachine.mu.RLock()
	defer s.dockerMachine.mu.RUnlock()
	if s.dockerMachine.bpf != nil {
		if config.IP4Subnet.IsValid() {
			ip, _ := config.HostIP4()
			err := s.dockerMachine.bpf.CfwdAddHostIP(ip)
			if err != nil {
				return fmt.Errorf("add host ip %+v to cfwd: %w", ip, err)
			}
		}
		if config.IP6Subnet.IsValid() {
			ip, _ := config.HostIP6()
			err := s.dockerMachine.bpf.CfwdAddHostIP(ip)
			if err != nil {
				return fmt.Errorf("add host ip %+v to cfwd: %w", ip, err)
			}
		}
	}

	return nil
}

func (s *SconGuestServer) DockerRemoveBridge(config sgtypes.DockerBridgeConfig, _ *None) error {
	// remove vmnet bridge on host, remove from VlanRouter, return vlan ID
	vlanId, err := s.m.host.RemoveDockerBridge(config)
	if err != nil {
		return err
	}

	// open nsfd
	initPidF := s.dockerMachine.initPidFile
	if initPidF == nil {
		return fmt.Errorf("docker machine has no init pid")
	}

	// now enter the container's netns...
	_, err = sysnet.WithNetns(initPidF, func() (struct{}, error) {
		// delete the link
		err := netlink.LinkDel(&netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{
				Name: fmt.Sprintf("%s%d", agent.DockerBridgeMirrorPrefix, vlanId),
			},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("delete mirror link: %w", err)
		}

		return struct{}{}, nil
	})
	if err != nil {
		return err
	}

	// remove host ip from cfwd bpf
	s.dockerMachine.mu.RLock()
	defer s.dockerMachine.mu.RUnlock()
	if s.dockerMachine.bpf != nil {
		if config.IP4Subnet.IsValid() {
			ip, _ := config.HostIP4()
			err := s.dockerMachine.bpf.CfwdRemoveHostIP(ip)
			if err != nil {
				return fmt.Errorf("remove host ip %+v from cfwd: %w", ip, err)
			}
		}
		if config.IP6Subnet.IsValid() {
			ip, _ := config.HostIP6()
			err := s.dockerMachine.bpf.CfwdRemoveHostIP(ip)
			if err != nil {
				return fmt.Errorf("remove host ip %+v from cfwd: %w", ip, err)
			}
		}
	}

	return nil
}

// note: this is for start/stop, not create/delete
func (s *SconGuestServer) OnDockerContainersChanged(diff sgtypes.Diff[dockertypes.ContainerSummaryMin], _ *None) error {
	// update mDNS registry
	for _, ctr := range diff.Added {
		s.m.net.mdnsRegistry.AddContainer(&ctr)
	}
	for _, ctr := range diff.Removed {
		s.m.net.mdnsRegistry.RemoveContainer(&ctr)
	}

	// attach cfwd to container net namespaces
	s.dockerMachine.mu.RLock()
	defer s.dockerMachine.mu.RUnlock()

	if s.dockerMachine.bpf != nil {
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

func mountOneNfsImage(img *dockertypes.FullImage) error {
	// guaranteed that there's a tag at this point
	tag := img.RepoTags[0]

	// c8d snapshotter not supported
	if img.GraphDriver.Name != "overlay2" {
		return nil
	}

	// open each dir as O_PATH fd. layer paths are too long so normally docker uses symlinks
	// TODO use proc root fd
	lowerDirValue := img.GraphDriver.Data["LowerDir"]
	lowerParts := strings.Split(lowerDirValue, ":")
	// make it empty?
	if lowerDirValue == "" {
		lowerParts = nil
	}
	layerDirs := make([]string, 0, 1+len(img.GraphDriver.Data))
	// upper first
	upperPath := strings.Replace(img.GraphDriver.Data["UpperDir"], "/var/lib/docker", conf.C().DockerDataDir, 1)
	// an image should never have no layers
	if upperPath == "" {
		return fmt.Errorf("image '%s' has no upper dir", tag)
	}

	upperFd, err := unix.Open(upperPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open upper dir '%s': %w", upperPath, err)
	}
	defer unix.Close(upperFd)
	// upper first, by order
	layerDirs = append(layerDirs, "/proc/self/fd/"+strconv.Itoa(upperFd))

	for _, dir := range lowerParts {
		lowerPath := strings.Replace(dir, "/var/lib/docker", conf.C().DockerDataDir, 1)
		lowerFd, err := unix.Open(lowerPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("open lower dir '%s': %w", lowerPath, err)
		}
		defer unix.Close(lowerFd)
		layerDirs = append(layerDirs, "/proc/self/fd/"+strconv.Itoa(lowerFd))
	}

	err = mountOneNfs("docker-image", "docker/images/"+tag, "overlay", unix.MS_RDONLY, "redirect_dir=nofollow,nfs_export=on,lowerdir="+strings.Join(layerDirs, ":"))
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}

	// add the export
	err = addNfsdExport(conf.C().NfsRootRO + "/docker/images/" + tag)
	if err != nil {
		return fmt.Errorf("add export: %w", err)
	}

	return nil
}

func (s *SconGuestServer) OnDockerImagesChanged(diff sgtypes.Diff[*dockertypes.FullImage], _ *None) error {
	// mount new ones
	for _, img := range diff.Added {
		err := mountOneNfsImage(img)
		if err != nil {
			logrus.WithError(err).Error("failed to mount docker image")
		}
	}

	// unmount old ones
	for _, img := range diff.Removed {
		// guaranteed that there's a tag at this point
		tag := img.RepoTags[0]
		err := unmountOneNfs("docker/images/" + tag)
		if err != nil {
			logrus.WithError(err).Error("failed to unmount docker image")
		}
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
	listener, err := listenUnixWithPerms(mounts.SconGuestSocket, 0600, 0, 0)
	if err != nil {
		return err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return nil
}
