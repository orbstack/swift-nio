package dockerdb

import (
	"encoding/json"
	"net/netip"

	"github.com/docker/libkv"
	"github.com/docker/libkv/store"
	"github.com/docker/libkv/store/boltdb"
)

type LibnetworkBridge struct {
	ID            string
	AddressIPv4   string
	DefaultBridge bool
}

func CheckBipNetworkConflict(dbPath string, configBip string) (*LibnetworkBridge, error) {
	// parse bip
	bipPrefix, err := netip.ParsePrefix(configBip)
	if err != nil {
		return nil, err
	}

	boltdb.Register()
	db, err := libkv.NewStore(store.BOLTDB, []string{dbPath}, &store.Config{
		Bucket: "libnetwork",
	})
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// find all the bridges
	bridgePairs, err := db.List("docker/network/v1.0/bridge/")
	if err != nil {
		return nil, err
	}
	for _, pair := range bridgePairs {
		// parse value
		var rawBridge LibnetworkBridge
		err := json.Unmarshal(pair.Value, &rawBridge)
		if err != nil {
			return nil, err
		}
		if rawBridge.AddressIPv4 == "" {
			continue
		}

		// parse address
		netPrefix, err := netip.ParsePrefix(rawBridge.AddressIPv4)
		if err != nil {
			return nil, err
		}

		// skip default
		if rawBridge.DefaultBridge {
			continue
		}

		// check for overlap
		if netPrefix.Overlaps(bipPrefix) {
			return &rawBridge, nil
		}
	}

	// no overlaps
	return nil, nil
}
