package main

import "github.com/orbstack/macvirt/vmgr/vzf"

func runUninstallPrivhelper() {
	err := vzf.SwextPrivhelperUninstall()
	check(err)
}
