package vnet

import (
	"sync"
	"time"

	"github.com/orbstack/macvirt/macvmgr/vnet/bridge"
	"github.com/orbstack/macvirt/macvmgr/vnet/netconf"
	"github.com/orbstack/macvirt/macvmgr/vzf"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
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

	logrus.Debug("creating scon machine host bridge")

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

// This recreates the bridge if the route to the machine subnet is wrong.
// Usually happens when VPN is enabled (changes to utun3) or when VPN is disabled (route is deleted, changes to en0).
// Since we don't have root, recreating the bridge is necessary to fix the route.
//
// Works because TCP flows, etc. don't get terminated. Just a brief ~100-200 ms of packet loss
func (n *Network) MonitorHostBridgeRoute() error {
	mon, err := bridge.NewRouteMon()
	if err != nil {
		return err
	}
	defer mon.Close()

	var recreateMu sync.Mutex
	recreateDebounce := syncx.NewFuncDebounce(100*time.Millisecond, func() {
		// ignore if we're already recreating
		// to avoid feedback loop
		if !recreateMu.TryLock() {
			return
		}
		defer recreateMu.Unlock()

		// check and skip if route is OK
		correct, err := bridge.IsMachineRouteCorrect()
		if err != nil {
			logrus.WithError(err).Error("failed to check machine host bridge route")
			return
		}
		if correct {
			return
		}

		err = n.CreateSconMachineHostBridge()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh machine host bridge")
		}
	})

	// TODO support stopping
	for {
		// monitor route changes to relevant subnet
		_, err := mon.Receive()
		if err != nil {
			return err
		}

		// kick route check
		recreateDebounce.Call()
	}
}
