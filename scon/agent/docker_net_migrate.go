package agent

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// from documentation test net 2
	// just temp, not actually used
	DockerNetMigrationBip  = "203.0.113.97/24"
	DockerNetMigrationFlag = "/etc/docker/.orb_migrate_networks"
)

func checkIPAMConflict(ipam dockertypes.IPAM, target netip.Prefix) (bool, error) {
	for _, config := range ipam.Config {
		logrus.WithField("config", config).Debug("checking IPAM config")
		subnet, err := netip.ParsePrefix(config.Subnet)
		if err != nil {
			return false, err
		}

		if subnet.Overlaps(target) {
			// we have a conflict
			return true, nil
		}
	}

	return false, nil
}

func (d *DockerAgent) getFullContainers() ([]*dockertypes.ContainerJSON, error) {
	var minContainers []*dockertypes.ContainerSummaryMin
	err := d.client.Call("GET", "/containers/json?all=true", nil, &minContainers)
	if err != nil {
		return nil, fmt.Errorf("get containers: %w", err)
	}

	var containers []*dockertypes.ContainerJSON
	for _, c := range minContainers {
		var full dockertypes.ContainerJSON
		err = d.client.Call("GET", "/containers/"+c.ID+"/json", nil, &full)
		if err != nil {
			return nil, fmt.Errorf("get container: %w", err)
		}

		containers = append(containers, &full)
	}

	return containers, nil
}

func (d *DockerAgent) migrateConflictNetworks(origConfigJson []byte) error {
	var origConfig map[string]any
	err := json.Unmarshal(origConfigJson, &origConfig)
	if err != nil {
		return err
	}

	targetBipStr := origConfig["bip"].(string)
	logrus.WithField("targetBip", targetBipStr).Info("migrating networks")

	// the bip we want. not the current temp one
	targetBip, err := netip.ParsePrefix(targetBipStr)
	if err != nil {
		return err
	}

	// get all networks
	var networks []dockertypes.Network
	err = d.client.Call("GET", "/networks", nil, &networks)
	if err != nil {
		return fmt.Errorf("get networks: %w", err)
	}

	// get all containers. need full info to get network aliases
	allContainers, err := d.getFullContainers()
	if err != nil {
		return fmt.Errorf("get containers: %w", err)
	}

	// find ones that conflict with bip prefix, and deal with them
	for _, minNet := range networks {
		// we only look at local bridges with IPv4 that conflicts w/ bip, and default IPAM driver
		if minNet.Scope != "local" || minNet.Driver != "bridge" || minNet.IPAM.Driver != "default" {
			continue
		}
		// check if conflict
		logrus.WithField("network", minNet.Name).Debug("checking network")
		hasConflict, err := checkIPAMConflict(minNet.IPAM, targetBip)
		if err != nil {
			return fmt.Errorf("check IPAM conflict: %w", err)
		}
		if !hasConflict {
			continue
		}

		// need to migrate this one
		logrus.WithField("network", minNet.Name).Info("migrating network")

		// fetch full info
		var fullNet dockertypes.Network
		err = d.client.Call("GET", "/networks/"+minNet.ID, nil, &fullNet)
		if err != nil {
			return fmt.Errorf("get network: %w", err)
		}

		// filter for containers
		// fullNet.Containers only includes running, so must look at allContainers
		netContainers := make(map[string]*dockertypes.NetworkEndpointSettings)
		for _, c := range allContainers {
			if c.NetworkSettings == nil {
				continue
			}
			if c.NetworkSettings.Networks == nil {
				continue
			}
			// don't trust the name, look through IDs
			for _, cnet := range c.NetworkSettings.Networks {
				if cnet.NetworkID == minNet.ID {
					netContainers[c.ID] = cnet
					break
				}
			}
		}

		// disconnect all containers
		logrus.WithField("network", minNet.Name).WithField("count", len(netContainers)).Info("disconnecting containers")
		for cid := range netContainers {
			logrus.WithField("cid", cid).Debug("disconnecting container")
			err = d.client.Call("POST", "/networks/"+minNet.ID+"/disconnect", dockertypes.NetworkDisconnectRequest{
				Container: cid,
				Force:     true,
			}, nil)
			if err != nil {
				// fatal. can't proceed if stuck
				return fmt.Errorf("disconnect container: %w", err)
			}
		}

		// delete the network
		err = d.client.Call("DELETE", "/networks/"+minNet.ID, nil, nil)
		if err != nil {
			return fmt.Errorf("delete network: %w", err)
		}

		// create new network with the same flags
		logrus.WithField("network", minNet.Name).Info("recreating network")
		var newNetResp dockertypes.NetworkCreateResponse
		newNetReq := fullNet
		newNetReq.ID = ""
		newNetReq.Created = ""
		newNetReq.Scope = ""
		newNetReq.Containers = nil
		newNetReq.CheckDuplicate = false // make sure it succeeds
		// discard conflicting IPv4 IPAM entries
		var newIPAMConfig []dockertypes.IPAMConfig
		for _, config := range newNetReq.IPAM.Config {
			subnet, err := netip.ParsePrefix(config.Subnet)
			if err != nil {
				return fmt.Errorf("parse subnet: %w", err)
			}

			if subnet.Overlaps(targetBip) {
				// we have a conflict
				continue
			}

			newIPAMConfig = append(newIPAMConfig, config)
		}
		newNetReq.IPAM.Config = newIPAMConfig
		err = d.client.Call("POST", "/networks/create", &newNetReq, &newNetResp)
		if err != nil {
			// oops, we probably ran out of pools...
			// try to restore the old one
			logrus.WithError(err).WithField("network", minNet.Name).Error("failed to recreate network, restoring")
			err = d.client.Call("POST", "/networks/create", &fullNet, &newNetResp)
			if err != nil {
				// fatal: if can't restore then it's broken
				return fmt.Errorf("restore network: %w", err)
			}

			// successfully restored. proceed to reconnect back, knowing that the migration failed to resolve conflicts
			// it's better than destroying data
		}

		// reconnect all containers
		logrus.WithField("network", minNet.Name).WithField("count", len(netContainers)).Info("reconnecting containers")
		for cid, endpointConfig := range netContainers {
			logrus.WithField("cid", cid).Debug("reconnecting container")
			err = d.client.Call("POST", "/networks/"+newNetResp.ID+"/connect", dockertypes.NetworkConnectRequest{
				Container: cid,
				EndpointConfig: &dockertypes.NetworkEndpointSettings{
					// only a few fields, exclude anything IP-related
					Links: endpointConfig.Links,
					// for Docker Compose sandbox DNS resolver
					Aliases: endpointConfig.Aliases,
				},
			}, nil)
			if err != nil {
				// not fatal but unexpected. too late to revert
				logrus.WithError(err).WithField("cid", cid).Error("failed to reconnect container")
			}
		}

		// fetch new full net to see where it went (for debug)
		var newFullNet dockertypes.Network
		err = d.client.Call("GET", "/networks/"+newNetResp.ID, nil, &newFullNet)
		if err != nil {
			return fmt.Errorf("get new network: %w", err)
		}

		logrus.WithField("from", minNet.IPAM.Config).WithField("to", newFullNet.IPAM.Config).Info("moved network")
	}

	// migration complete. remove flag, rewrite config, and restart dockerd
	logrus.Info("migration complete, restarting")
	err = os.Remove(DockerNetMigrationFlag)
	if err != nil {
		return err
	}

	// restore orig config to set correct bip & pools
	err = os.WriteFile("/etc/docker/daemon.json", origConfigJson, 0644)
	if err != nil {
		return err
	}

	// restart dockerd:
	// tini > simplevisor > dockerd
	// first delete socket to prevent race when PreStart is called again
	_ = os.Remove("/var/run/docker.sock")
	// kill tini with SIGUSR2. it'll forward
	err = unix.Kill(1, unix.SIGUSR2)
	if err != nil {
		return err
	}

	// kill containers to speed it up
	err = d.killContainers()
	if err != nil {
		return err
	}

	return nil
}
