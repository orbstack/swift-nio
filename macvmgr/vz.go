package main

import (
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmgr/arch"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
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
	NetworkGvnet     bool
	NetworkNat       bool
	NetworkPairFd    *os.File
	MacAddressPrefix string
	Balloon          bool
	Rng              bool
	Vsock            bool
	Virtiofs         bool
	Rosetta          bool
	Sound            bool
}

func findBestMtu() int {
	if err := vz.MacOSAvailable(13); err != nil {
		return 1500
	} else {
		return vnet.PreferredMtu // prefer 65520
	}
}

func clamp[T uint | uint64](n, min, max T) T {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func CreateVm(c *VmParams) (*vnet.Network, *vz.VirtualMachine) {
	cmdline := []string{
		// boot
		"init=/opt/orb/preinit",
		// userspace
		"orb.data_size=" + strconv.FormatUint(conf.DiskSize(), 10),
		// Kernel tuning
		"workqueue.power_efficient=1",
		"cgroup.memory=nokmem,nosocket",
		// rcu_nocbs is in kernel
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

	bootloader, err := vz.NewLinuxBootLoader(
		c.Kernel,
		vz.WithCommandLine(strings.Join(cmdline, " ")),
	)
	check(err)

	config, err := vz.NewVirtualMachineConfiguration(
		bootloader,
		clamp(uint(c.Cpus), vz.VirtualMachineConfigurationMinimumAllowedCPUCount(), vz.VirtualMachineConfigurationMaximumAllowedCPUCount()),
		clamp(c.Memory*1024*1024, vz.VirtualMachineConfigurationMinimumAllowedMemorySize(), vz.VirtualMachineConfigurationMaximumAllowedMemorySize()),
	)
	check(err)

	// Console
	if c.Console != ConsoleNone {
		var read, write *os.File
		switch c.Console {
		case ConsoleStdio:
			read = os.Stdin
			write = os.Stdout
		case ConsoleLog:
			read, err = os.Open("/dev/null")
			check(err)
			write, err = os.Create(conf.ConsoleLog())
			check(err)
		}

		serialConsoleFds, err := vz.NewFileHandleSerialPortAttachment(read, write)
		check(err)
		serialConsole, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialConsoleFds)
		check(err)
		config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{
			serialConsole,
		})
	}

	// Network
	netDevices := []*vz.VirtioNetworkDeviceConfiguration{}
	mtu := findBestMtu()
	// 1. gvnet
	var vnetwork *vnet.Network
	if c.NetworkGvnet {
		newNetwork, gvnetFile, err := vnet.StartUnixgramPair(vnet.NetOptions{
			MTU: uint32(mtu),
		})
		vnetwork = newNetwork
		check(err)
		attachment, err := vz.NewFileHandleNetworkDeviceAttachment(gvnetFile)
		check(err)
		_ = attachment.SetMaximumTransmissionUnit(mtu) // ignore: err on macOS 12
		network, err := vz.NewVirtioNetworkDeviceConfiguration(attachment)
		check(err)
		macAddr, err := net.ParseMAC(c.MacAddressPrefix + ":01")
		check(err)
		mac, err := vz.NewMACAddress(macAddr)
		check(err)
		network.SetMACAddress(mac)
		netDevices = append(netDevices, network)
	}

	// 2. NAT
	if c.NetworkNat {
		attachment, err := vz.NewNATNetworkDeviceAttachment()
		check(err)
		network, err := vz.NewVirtioNetworkDeviceConfiguration(attachment)
		check(err)
		macAddr, err := net.ParseMAC(c.MacAddressPrefix + ":02")
		check(err)
		mac, err := vz.NewMACAddress(macAddr)
		check(err)
		network.SetMACAddress(mac)
		netDevices = append(netDevices, network)
	}

	// 3. pair
	if c.NetworkPairFd != nil {
		handleNet, err := vz.NewFileHandleNetworkDeviceAttachment(c.NetworkPairFd)
		check(err)
		handleNet.SetMaximumTransmissionUnit(mtu)
		network, err := vz.NewVirtioNetworkDeviceConfiguration(handleNet)
		check(err)
		macAddr, err := net.ParseMAC(c.MacAddressPrefix + ":03")
		check(err)
		mac, err := vz.NewMACAddress(macAddr)
		check(err)
		network.SetMACAddress(mac)
		netDevices = append(netDevices, network)
	}

	config.SetNetworkDevicesVirtualMachineConfiguration(netDevices)

	// RNG
	if c.Rng {
		rng, err := vz.NewVirtioEntropyDeviceConfiguration()
		check(err)
		config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{
			rng,
		})
	}

	// Disks (raw!)
	storages := []vz.StorageDeviceConfiguration{}
	// 1. rootfs
	if c.DiskRootfs != "" {
		disk1, err := vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(
			c.DiskRootfs,
			true, // read-only
			vz.DiskImageCachingModeCached,
			vz.DiskImageSynchronizationModeFsync,
		)
		check(err)
		storage1, err := vz.NewVirtioBlockDeviceConfiguration(disk1)
		check(err)
		storages = append(storages, storage1)
	}
	// 2. data
	if c.DiskData != "" {
		disk2, err := vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(
			c.DiskData,
			false,
			// cache for perf
			vz.DiskImageCachingModeCached,
			// fsync for safety, but not full fsync (degrades to 50-75 IOPS)
			vz.DiskImageSynchronizationModeFsync,
		)
		check(err)
		storage2, err := vz.NewVirtioBlockDeviceConfiguration(disk2)
		check(err)
		storages = append(storages, storage2)
	}
	// 3. swap
	if c.DiskSwap != "" {
		disk3, err := vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(
			c.DiskSwap,
			false,
			vz.DiskImageCachingModeCached,
			// no point in fsyncing swap. we'll never use it again after reboot
			vz.DiskImageSynchronizationModeNone,
		)
		check(err)
		storage3, err := vz.NewVirtioBlockDeviceConfiguration(disk3)
		check(err)
		storages = append(storages, storage3)
	}
	config.SetStorageDevicesVirtualMachineConfiguration(storages)

	// Balloon
	if c.Balloon {
		balloon, err := vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration()
		check(err)
		config.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{
			balloon,
		})
	}

	// Vsock
	if c.Vsock {
		vsock, err := vz.NewVirtioSocketDeviceConfiguration()
		check(err)
		config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{
			vsock,
		})
	}

	validated, err := config.Validate()
	check(err)
	if !validated {
		logrus.Fatal("validation failed", err)
	}

	// virtiofs (shared)
	fsDevices := []vz.DirectorySharingDeviceConfiguration{}
	if c.Virtiofs {
		virtiofs, err := vz.NewVirtioFileSystemDeviceConfiguration("mac")
		check(err)
		hostDir, err := vz.NewSharedDirectory("/", false)
		check(err)
		hostDirShare, err := vz.NewSingleDirectoryShare(hostDir)
		check(err)
		virtiofs.SetDirectoryShare(hostDirShare)
		fsDevices = append(fsDevices, *virtiofs)
	}

	// Rosetta (virtiofs)
	if c.Rosetta && vmconfig.Get().Rosetta {
		result, err := arch.CreateRosettaDevice()
		check(err)
		if result != nil && result.FsDevice != nil {
			fsDevices = append(fsDevices, *result.FsDevice)
		}
		if result != nil && result.InstallCanceled {
			logrus.Info("user canceled Rosetta install, saving preference")
			err := vmconfig.Update(func(c *vmconfig.VmConfig) {
				c.Rosetta = false
			})
			check(err)
		}
	}

	config.SetDirectorySharingDevicesVirtualMachineConfiguration(fsDevices)

	// Sound
	if c.Sound {
		sound, err := vz.NewVirtioSoundDeviceConfiguration()
		check(err)
		soundOutput, err := vz.NewVirtioSoundDeviceHostOutputStreamConfiguration()
		check(err)
		sound.SetStreams(soundOutput)
		config.SetAudioDevicesVirtualMachineConfiguration([]vz.AudioDeviceConfiguration{
			sound,
		})
	}

	// Boot!
	vm, err := vz.NewVirtualMachine(config)
	check(err)

	return vnetwork, vm
}
