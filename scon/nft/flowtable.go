package nft

import (
	"fmt"
	"slices"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
)

func UpdateFlowtable(tableName string, ftName string, interfaces []string) error {
	return WithTable(FamilyInet, tableName, func(conn *nftables.Conn, table *nftables.Table) error {
		// returns flowtable
		_ = conn.AddFlowtable(&nftables.Flowtable{
			Table: table,
			Name:  ftName,
			// hook ingress priority filter;
			Hooknum:  nftables.FlowtableHookIngress,
			Priority: nftables.FlowtablePriorityFilter,
			Devices:  interfaces,
			Flags:    0,
		})

		return nil
	})
}

// RefreshFlowtableBridgePorts updates the flowtable with all ports attached to the given bridges, as well as the forwarding ports.
// Needed because flowtables can only act as a fastpath bypass when attached directly to port netdev ingress hooks
func RefreshFlowtableBridgePorts(tableName string, ftName string, bridges []string, forwardingPorts []string, excludePorts []string) error {
	// get all interfaces (we need both bridges and ports)
	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("list links: %w", err)
	}

	// pass 1: resolve bridge names to indexes
	// maps are slow, but better than potential O(n^2) if users have crazy numbers of containers and bridges
	bridgeNames := make(map[string]struct{}, len(bridges))
	for _, b := range bridges {
		bridgeNames[b] = struct{}{}
	}
	bridgeIndexes := make(map[int]struct{}, len(bridges))
	for _, l := range links {
		attrs := l.Attrs()
		if _, ok := bridgeNames[attrs.Name]; ok {
			bridgeIndexes[attrs.Index] = struct{}{}
		}
	}

	// pass 2: find bridge ports
	bridgePorts := make([]string, 0, len(links))
	for _, l := range links {
		attrs := l.Attrs()
		if _, ok := bridgeIndexes[attrs.MasterIndex]; ok {
			if !slices.Contains(excludePorts, attrs.Name) {
				bridgePorts = append(bridgePorts, attrs.Name)
			}
		}
	}
	bridgePorts = append(bridgePorts, forwardingPorts...)

	// update flowtable
	err = UpdateFlowtable(tableName, ftName, bridgePorts)
	if err != nil {
		return fmt.Errorf("update nft interfaces: %w", err)
	}

	return nil
}
