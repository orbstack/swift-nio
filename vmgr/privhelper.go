package main

import (
	"encoding/json"
	"os"

	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vzf"
)

func runUninstallPrivhelper() {
	err := vzf.SwextPrivhelperUninstall()
	check(err)
}

func runSetRefreshToken() {
	err := drmcore.SaveRefreshToken(os.Args[1])
	check(err)
}

func runCheckRefreshToken() {
	data, err := drmcore.ReadKeychainDrmState()
	if err == nil {
		var state drmtypes.PersistentState
		err = json.Unmarshal(data, &state)
		if err == nil && state.RefreshToken != "" {
			os.Exit(0)
		}
	}

	os.Exit(1)
}
