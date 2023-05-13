package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/macvmgr/conf"
	"github.com/orbstack/macvirt/macvmgr/osver"
	"github.com/orbstack/macvirt/macvmgr/vmconfig"
	"github.com/orbstack/macvirt/macvmgr/vnet"
	"github.com/orbstack/macvirt/macvmgr/vzf"
	"github.com/sirupsen/logrus"
)

type ConsoleMode int

const (
	ConsoleNone ConsoleMode = iota
	ConsoleStdio
	ConsoleLog
)

type VmParams struct {
	Cpus             int
	Memory           uint64
	Kernel           string
	Console          ConsoleMode
	DiskRootfs       string
	DiskData         string
	DiskSwap         string
	NetworkVnet      bool
	NetworkNat       bool
	NetworkPairFile  *os.File
	MacAddressPrefix string
	Balloon          bool
	Rng              bool
	Vsock            bool
	Virtiofs         bool
	Rosetta          bool
	Sound            bool
}

func findBestMtu() int {
	if osver.IsAtLeast("v13.0") {
		return vnet.PreferredMTU
	} else {
		return vnet.BaseMTU
	}
}

func CreateVm(c *VmParams) (*vnet.Network, *vzf.Machine) {
	cmdline := []string{
		// boot
		"init=/opt/orb/preinit",
		// userspace
		"orb.data_size=" + strconv.FormatUint(conf.DiskSize(), 10),
		// Kernel tuning
		"workqueue.power_efficient=1",
		"cgroup.memory=nokmem,nosocket",
		// rcu_nocbs is in kernel
		// Drivers
		"nbd.nbds_max=4", // fast boot
	}
	if runtime.GOARCH == "amd64" {
		// on ARM: kpti is free with E0PD
		// But on x86, there are too many, just disable it like Docker
		// Also prevent TSC from being disabled after sleep with tsc=reliable
		cmdline = append(cmdline, "mitigations=off", "clocksource=tsc", "tsc=reliable")
	}
	if c.DiskRootfs != "" {
		cmdline = append(cmdline, "root=/dev/vda", "rootfstype=erofs", "ro")
	}
	if c.Console != ConsoleNone {
		cmdline = append(cmdline, "console=hvc0")
	}
	logrus.Debug("cmdline", cmdline)

	spec := vzf.VzSpec{
		Cpus:             c.Cpus,
		Memory:           c.Memory * 1024 * 1024,
		Kernel:           c.Kernel,
		Cmdline:          strings.Join(cmdline, " "),
		MacAddressPrefix: c.MacAddressPrefix,
		NetworkNat:       c.NetworkNat,
		Rng:              c.Rng,
		DiskRootfs:       c.DiskRootfs,
		DiskData:         c.DiskData,
		DiskSwap:         c.DiskSwap,
		Balloon:          c.Balloon,
		Vsock:            c.Vsock,
		Virtiofs:         c.Virtiofs,
		Rosetta:          c.Rosetta,
		Sound:            c.Sound,
	}

	// Console
	var err error
	retainFiles := []*os.File{}
	if c.Console != ConsoleNone {
		var conRead, conWrite *os.File
		switch c.Console {
		case ConsoleStdio:
			conRead = os.Stdin
			conWrite = os.Stdout
		case ConsoleLog:
			conRead, err = os.Open("/dev/null")
			check(err)
			conWrite, err = NewConsoleLogPipe()
			check(err)
		}

		spec.Console = &vzf.ConsoleSpec{
			ReadFd:  int(conRead.Fd()),
			WriteFd: int(conWrite.Fd()),
		}
		retainFiles = append(retainFiles, conRead, conWrite)
	}

	// Network
	mtu := findBestMtu()
	spec.Mtu = mtu
	// gvnet
	var vnetwork *vnet.Network
	if c.NetworkVnet {
		newNetwork, gvnetFile, err := vnet.StartUnixgramPair(vnet.NetOptions{
			LinkMTU: uint32(mtu),
		})
		check(err)
		vnetwork = newNetwork

		// TODO: set nonblock using util.GetFd? doesn't seem to do anything
		fd := int(gvnetFile.Fd())
		spec.NetworkVnetFd = &fd
		// already retained by network, but doesn't hurt
		retainFiles = append(retainFiles, gvnetFile)
	}
	if c.NetworkPairFile != nil {
		fd := int(c.NetworkPairFile.Fd())
		spec.NetworkVnetFd = &fd
		retainFiles = append(retainFiles, c.NetworkPairFile)
	}

	// must retain files or they get closed by Go finalizer!
	// causes flaky console
	vm, rosettaCanceled, err := vzf.NewMachine(spec, retainFiles)
	check(err)

	if rosettaCanceled {
		logrus.Info("user canceled Rosetta install, saving preference")
		err := vmconfig.Update(func(c *vmconfig.VmConfig) {
			c.Rosetta = false
		})
		check(err)
	}

	return vnetwork, vm
}
