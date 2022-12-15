package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/kdrag0n/vz-macvirt/v3"
)

type VmConfig struct {
	Cpus             int
	Memory           uint64
	Kernel           string
	Console          bool
	DiskRootfs       string
	DiskData         string
	DiskSwap         string
	NetworkNat       bool
	NetworkGvproxy   bool
	NetworkPairFd    *os.File
	MacAddressPrefix string
	Balloon          bool
	Rng              bool
	Vsock            bool
	Virtiofs         bool
	Rosetta          bool
	Sound            bool
}

func CreateVm(c *VmConfig) *vz.VirtualMachine {
	cmdline := strings.Join([]string{
		// boot
		"root=/dev/vda",
		"rootfstype=erofs",
		"ro",
		"init=/opt/vc/preinit",
		"console=hvc0",
		// Kernel tuning
		"rcu_nocbs=0-" + strconv.Itoa(c.Cpus-1),
		"workqueue.power_efficient=1",
		"cgroup.memory=nokmem,nosocket",
		//"mitigations=off", // free with e0pd
		// userspace
		"vc.data_size=65536",
		"vc.vcontrol_token=test",
		"vc.timezone=America/Los_Angeles",
	}, " ")
	fmt.Println("cmdline", cmdline)

	bootloader, err := vz.NewLinuxBootLoader(
		c.Kernel,
		vz.WithCommandLine(cmdline),
	)
	check(err)

	config, err := vz.NewVirtualMachineConfiguration(
		bootloader,
		uint(c.Cpus),
		c.Memory*1024*1024,
	)
	check(err)

	// Console
	if c.Console {
		serialConsoleFds, err := vz.NewFileHandleSerialPortAttachment(os.Stdin, os.Stdout)
		check(err)
		serialConsole, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialConsoleFds)
		check(err)
		config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{
			serialConsole,
		})
	}

	// Network
	netDevices := []*vz.VirtioNetworkDeviceConfiguration{}
	if c.NetworkNat {
		nat, err := vz.NewNATNetworkDeviceAttachment()
		check(err)
		network1, err := vz.NewVirtioNetworkDeviceConfiguration(nat)
		check(err)
		macAddr, err := net.ParseMAC(c.MacAddressPrefix + ":00")
		check(err)
		mac, err := vz.NewMACAddress(macAddr)
		check(err)
		network1.SetMACAddress(mac)
	}

	if c.NetworkGvproxy {
		gvproxyFile, err := startGvproxyPair()
		check(err)
		handleNet, err := vz.NewFileHandleNetworkDeviceAttachment(gvproxyFile)
		handleNet.SetMaximumTransmissionUnit(gvproxyMtu)
		check(err)
		network2, err := vz.NewVirtioNetworkDeviceConfiguration(handleNet)
		check(err)
		macAddr2, err := net.ParseMAC(c.MacAddressPrefix + ":01")
		check(err)
		mac2, err := vz.NewMACAddress(macAddr2)
		check(err)
		network2.SetMACAddress(mac2)
	}

	if c.NetworkPairFd != nil {
		handleNet, err := vz.NewFileHandleNetworkDeviceAttachment(c.NetworkPairFd)
		check(err)
		network3, err := vz.NewVirtioNetworkDeviceConfiguration(handleNet)
		check(err)
		macAddr3, err := net.ParseMAC(c.MacAddressPrefix + ":02")
		check(err)
		mac3, err := vz.NewMACAddress(macAddr3)
		check(err)
		network3.SetMACAddress(mac3)
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
		disk1, err := vz.NewDiskImageStorageDeviceAttachment(
			c.DiskRootfs,
			true,
		)
		check(err)
		storage1, err := vz.NewVirtioBlockDeviceConfiguration(disk1)
		check(err)
		storages = append(storages, storage1)
	}
	// 2. data
	if c.DiskData != "" {
		disk2, err := vz.NewDiskImageStorageDeviceAttachment(
			c.DiskData,
			false,
		)
		check(err)
		storage2, err := vz.NewVirtioBlockDeviceConfiguration(disk2)
		check(err)
		storages = append(storages, storage2)
	}
	// 3. swap
	if c.DiskSwap != "" {
		disk3, err := vz.NewDiskImageStorageDeviceAttachment(
			c.DiskSwap,
			false,
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
		log.Fatal("validation failed", err)
	}

	// virtiofs (shared)
	fsDevices := []vz.DirectorySharingDeviceConfiguration{}
	if c.Virtiofs {
		virtiofs, err := vz.NewVirtioFileSystemDeviceConfiguration("shared")
		check(err)
		hostDir, err := vz.NewSharedDirectory("/", false)
		check(err)
		hostDirShare, err := vz.NewSingleDirectoryShare(hostDir)
		check(err)
		virtiofs.SetDirectoryShare(hostDirShare)
		fsDevices = append(fsDevices, *virtiofs)
	}

	// Rosetta (virtiofs)
	if c.Rosetta {
		switch vz.LinuxRosettaDirectoryShareAvailability() {
		case vz.LinuxRosettaAvailabilityNotInstalled:
			err = vz.LinuxRosettaDirectoryShareInstallRosetta()
			check(err)
			fallthrough
		case vz.LinuxRosettaAvailabilityInstalled:
			rosettaDir, err := vz.NewLinuxRosettaDirectoryShare()
			check(err)
			virtiofsRosetta, err := vz.NewVirtioFileSystemDeviceConfiguration("rosetta")
			virtiofsRosetta.SetDirectoryShare(rosettaDir)
			fsDevices = append(fsDevices, *virtiofsRosetta)
		}
	}

	config.SetDirectorySharingDevicesVirtualMachineConfiguration(fsDevices)

	// Sound
	if c.Sound {
		sound, err := vz.NewVirtioSoundDeviceConfiguration()
		check(err)
		soundOutput, err := vz.NewVirtioSoundDeviceHostOutputStreamConfiguration()
		sound.SetStreams(soundOutput)
		config.SetAudioDevicesVirtualMachineConfiguration([]vz.AudioDeviceConfiguration{
			sound,
		})
	}

	// Boot!
	vm, err := vz.NewVirtualMachine(config)
	check(err)

	return vm
}
