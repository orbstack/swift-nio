package vmgr

import "github.com/orbstack/macvirt/vmgr/swext"

func runUninstallPrivhelper() {
	err := swext.SwextPrivhelperUninstall()
	check(err)
}
