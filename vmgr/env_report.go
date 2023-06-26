package main

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
)

// this is in here instead of orbctl because we're the one doing setup
func runReportEnv() {
	client := vmclient.Client()
	report := &vmtypes.EnvReport{
		Environ: os.Environ(),
	}
	err := client.InternalReportEnv(report)
	check(err)
}
