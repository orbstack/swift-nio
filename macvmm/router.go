package main

import (
	"os"
	"runtime"

	"github.com/kdrag0n/vz-macvirt/v3"
)

func StartRouterVm(netPairs []*os.File) *vz.VirtualMachine {
	config := &VmConfig{
		Cpus:             runtime.NumCPU(),
		Memory:           800,
		Kernel:           "../assets_router/kernel",
		Console:          true,
		DiskRootfs:       "../assets_router/rootfs.img",
		DiskData:         "../assets_router/data.img",
		NetworkNat:       true,
		NetworkPairFds:   netPairs,
		MacAddressPrefix: "86:6c:f1:2e:9f",
		Balloon:          true,
		Rng:              true,
	}

	vm := CreateVm(config)
	err := vm.Start()
	check(err)

	return vm
}
