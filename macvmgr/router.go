package main

import (
	"os"
	"runtime"

	"github.com/Code-Hex/vz/v3"
)

func StartRouterVm(netPair2 *os.File) *vz.VirtualMachine {
	config := &VmParams{
		Cpus:             runtime.NumCPU(),
		Memory:           800,
		Kernel:           "../assets_router/kernel",
		Console:          ConsoleNone,
		DiskRootfs:       "../assets_router/rootfs.img",
		DiskData:         "../assets_router/data.img",
		NetworkNat:       true,
		NetworkPairFd:    netPair2,
		MacAddressPrefix: "86:6c:f1:2e:9f",
		Balloon:          true,
		Rng:              true,
	}

	_, vm := CreateVm(config)
	err := vm.Start()
	check(err)

	return vm
}
