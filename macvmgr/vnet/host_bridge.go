package vnet

import (
	"github.com/orbstack/macvirt/macvmgr/vnet/netconf"
	"github.com/orbstack/macvirt/macvmgr/vzf"
)

const (
	brIndexSconMachine = iota
	brIndexDocker

	brUuidSconMachine = "25ef1ee1-1ead-40fd-a97d-f9284917459b"
	brUuidDocker      = "8bd4b797-07cc-4118-9147-d2e349132a12"
)

func (n *Network) AddHostBridgeFd(fd int) {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	n.hostBridgeFds = append(n.hostBridgeFds, fd)
	n.hostBridges = append(n.hostBridges, nil)
}

// TODO remove dependency on vzf
func (n *Network) CreateSconMachineHostBridge() error {
	n.hostBridgeMu.Lock()
	defer n.hostBridgeMu.Unlock()

	// recreate if needed
	oldBrnet := n.hostBridges[brIndexSconMachine]
	if oldBrnet != nil {
		oldBrnet.Close()
	}

	return n.createHostBridge(brIndexSconMachine, vzf.BridgeNetworkConfig{
		TapFd: n.hostBridgeFds[brIndexSconMachine],

		UUID:       brUuidSconMachine,
		Ip4Address: netconf.SconHostBridgeIP4,
		Ip4Mask:    "255.255.255.0",
		Ip6Address: netconf.SconHostBridgeIP6,

		MaxLinkMTU: int(n.LinkMTU),
	})
}

func (n *Network) createHostBridge(index int, config vzf.BridgeNetworkConfig) error {
	brnet, err := vzf.SwextNewBrnet(config)
	if err != nil {
		return err
	}

	n.hostBridges[index] = brnet
	return nil
}
