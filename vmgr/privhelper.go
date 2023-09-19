package main

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
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
