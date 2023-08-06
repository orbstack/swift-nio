package main

import (
	"fmt"
	"net"
	"net/rpc"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
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
	la.Name = fmt.Sprintf(".orbmirror%d", vlanId)
	la.ParentIndex = s.vlanRouterIfi // parent = eth2
	la.MTU = 1500                    // doesn't really matter because GSO
	// move to container netns (doesn't accept pidfd as nsfd)
	la.Namespace = netlink.NsPid(s.dockerMachine.lxc.InitPid())
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
				Name: fmt.Sprintf(".orbmirror%d", vlanId),
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

	return nil
}

// note: this is for start/stop, not create/delete
func (s *SconGuestServer) OnDockerContainersChanged(diff sgtypes.DockerContainersDiff, _ *None) error {
	// update mDNS registry
	for _, ctr := range diff.Added {
		s.m.net.mdnsRegistry.AddContainer(&ctr)
	}
	for _, ctr := range diff.Removed {
		s.m.net.mdnsRegistry.RemoveContainer(&ctr)
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
