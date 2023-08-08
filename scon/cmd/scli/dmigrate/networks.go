package dmigrate

import (
	"fmt"
	"net/netip"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func (m *Migrator) migrateOneNetwork(n dockertypes.Network) error {
	logrus.Infof("Migrating network %s", n.Name)

	// create network on dest, mostly same flags
	var newNetResp dockertypes.NetworkCreateResponse
	newNetReq := n
	newNetReq.ID = ""
	newNetReq.Created = ""
	newNetReq.Scope = ""
	newNetReq.Containers = nil
	newNetReq.CheckDuplicate = true // don't want dupe nets

	// if it's default Compose, then we can discard ipv4 net and use more-compatible net
	if n.Labels["com.docker.compose.network"] == "default" {
		var newIPAMConfig []dockertypes.IPAMConfig
		for _, config := range newNetReq.IPAM.Config {
			subnet, err := netip.ParsePrefix(config.Subnet)
			if err != nil {
				continue
			}

			// discard ipv4
			if subnet.Addr().Is4() {
				continue
			}

			newIPAMConfig = append(newIPAMConfig, config)
		}
		newNetReq.IPAM.Config = newIPAMConfig
	}

	err := m.destClient.Call("POST", "/networks/create", newNetReq, &newNetResp)
	if err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	// save ID
	m.mu.Lock()
	m.networkIDMap[n.ID] = newNetResp.ID
	m.mu.Unlock()

	return nil
}

func (m *Migrator) submitNetworks(group *pond.TaskGroup, networks []dockertypes.Network) error {
	for _, n := range networks {
		n := n
		logrus.WithField("network", n.Name).Debug("submitting network")
		group.Submit(func() {
			defer m.finishOneEntity(&entitySpec{networkName: n.Name})

			err := m.migrateOneNetwork(n)
			if err != nil {
				panic(fmt.Errorf("network %s: %w", n.Name, err))
			}
		})
	}

	return nil
}
