package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/Code-Hex/vz/v3"
	"github.com/pkg/term/termios"
	"golang.org/x/sys/unix"
)

// https://developer.apple.com/documentation/virtualization/running_linux_in_a_virtual_machine?language=objc#:~:text=Configure%20the%20Serial%20Port%20Device%20for%20Standard%20In%20and%20Out
func setRawMode(f *os.File) *unix.Termios {
	var oldAttr unix.Termios
	var attr unix.Termios

	// Get settings for terminal
	// this still isn't raw mode, still converts ^C, so we set new one
	termios.Tcgetattr(f.Fd(), &oldAttr)

	// Put stdin into raw mode, disabling local echo, input canonicalization,
	// and CR-NL mapping.
	attr.Iflag &^= syscall.ICRNL
	attr.Lflag &^= syscall.ICANON | syscall.ECHO

	// Set minimum characters when reading = 1 char
	attr.Cc[syscall.VMIN] = 1

	// set timeout when reading as non-canonical mode
	attr.Cc[syscall.VTIME] = 0

	// reflects the changed settings
	termios.Tcsetattr(f.Fd(), termios.TCSANOW, &attr)

	return &oldAttr
}

func revertRawMode(f *os.File, oldAttr *unix.Termios) {
	termios.Tcsetattr(f.Fd(), termios.TCSANOW, oldAttr)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	cmdline := strings.Join([]string{
		// boot
		"root=/dev/vda",
		"rootfstype=erofs",
		"ro",
		"init=/opt/vc/preinit",
		"console=hvc0",
		// kernel tuning
		"rcu_nocbs=0-7",
		"workqueue.power_efficient=1",
		"cgroup.memory=nokmem,nosocket",
		//"mitigations=off", // free with e0pd
		// userspace
		"vc.data_size=10240",
		"vc.vcontrol_token=test",
		"vc.timezone=America/Los_Angeles",
	}, " ")
	fmt.Println("cmdline", cmdline)

	bootloader, err := vz.NewLinuxBootLoader(
		"../assets/kernel",
		vz.WithCommandLine(cmdline),
	)
	check(err)
	fmt.Println("bootloader", bootloader)

	config, err := vz.NewVirtualMachineConfiguration(
		bootloader,
		uint(runtime.NumCPU()),
		1200*1024*1024,
	)
	check(err)

	// Console
	serialConsoleFds, err := vz.NewFileHandleSerialPortAttachment(os.Stdin, os.Stdout)
	check(err)
	serialConsole, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialConsoleFds)
	check(err)
	config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{
		serialConsole,
	})

	// Network
	nat, err := vz.NewNATNetworkDeviceAttachment()
	check(err)
	network1, err := vz.NewVirtioNetworkDeviceConfiguration(nat)
	check(err)
	macAddr, err := net.ParseMAC("86:6c:f1:2e:9e:1e")
	check(err)
	mac, err := vz.NewMACAddress(macAddr)
	check(err)
	network1.SetMACAddress(mac)

	gvproxyFile, err := startGvproxyPair()
	check(err)
	handleNet, err := vz.NewFileHandleNetworkDeviceAttachment(gvproxyFile)
	handleNet.SetMaximumTransmissionUnit(gvproxyMtu)
	check(err)
	network2, err := vz.NewVirtioNetworkDeviceConfiguration(handleNet)
	check(err)
	macAddr2, err := net.ParseMAC("86:6c:f1:2e:9e:1f")
	check(err)
	mac2, err := vz.NewMACAddress(macAddr2)
	check(err)
	network2.SetMACAddress(mac2)

	config.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{
		network1,
		network2,
	})

	// RNG
	rng, err := vz.NewVirtioEntropyDeviceConfiguration()
	check(err)
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{
		rng,
	})

	// Disks (raw!)
	// 1. rootfs
	disk1, err := vz.NewDiskImageStorageDeviceAttachment(
		"../assets/rootfs.img",
		true,
	)
	check(err)
	storage1, err := vz.NewVirtioBlockDeviceConfiguration(disk1)
	check(err)
	// 2. data
	disk2, err := vz.NewDiskImageStorageDeviceAttachment(
		"../assets/data.img",
		false,
	)
	check(err)
	storage2, err := vz.NewVirtioBlockDeviceConfiguration(disk2)
	check(err)
	// 3. swap
	disk3, err := vz.NewDiskImageStorageDeviceAttachment(
		"../assets/swap.img",
		false,
	)
	check(err)
	storage3, err := vz.NewVirtioBlockDeviceConfiguration(disk3)
	check(err)

	config.SetStorageDevicesVirtualMachineConfiguration([]vz.StorageDeviceConfiguration{
		storage1,
		storage2,
		storage3,
	})

	// Balloon
	balloon, err := vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration()
	check(err)
	config.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{
		balloon,
	})

	// Vsock
	vsock, err := vz.NewVirtioSocketDeviceConfiguration()
	check(err)
	config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{
		vsock,
	})
	validated, err := config.Validate()
	check(err)
	if !validated {
		log.Fatal("validation failed", err)
	}

	// virtiofs (shared)
	virtiofs, err := vz.NewVirtioFileSystemDeviceConfiguration("shared")
	check(err)
	hostDir, err := vz.NewSharedDirectory("/", false)
	check(err)
	hostDirShare, err := vz.NewSingleDirectoryShare(hostDir)
	check(err)
	virtiofs.SetDirectoryShare(hostDirShare)
	virtiofsDevices := []vz.DirectorySharingDeviceConfiguration{
		*virtiofs,
	}

	// Rosetta (virtiofs)
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
		virtiofsDevices = append(virtiofsDevices, *virtiofsRosetta)
	}

	config.SetDirectorySharingDevicesVirtualMachineConfiguration(virtiofsDevices)

	// Sound
	sound, err := vz.NewVirtioSoundDeviceConfiguration()
	check(err)
	soundOutput, err := vz.NewVirtioSoundDeviceHostOutputStreamConfiguration()
	sound.SetStreams(soundOutput)
	config.SetAudioDevicesVirtualMachineConfiguration([]vz.AudioDeviceConfiguration{
		sound,
	})

	// Boot!
	vm, err := vz.NewVirtualMachine(config)
	check(err)

	// Listen for signals
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM)

	err = vm.Start()
	check(err)

	oldAttr := setRawMode(os.Stdin)
	defer revertRawMode(os.Stdin, oldAttr)

	errCh := make(chan error, 1)

	for {
		select {
		case <-signalCh:
			log.Println("recieved signal")
			err := vm.Stop()
			if err != nil {
				log.Println("request stop error:", err)
				return
			}
		case newState := <-vm.StateChangedNotify():
			if newState == vz.VirtualMachineStateRunning {
				log.Println("start VM is running")
			}
			if newState == vz.VirtualMachineStateStopped {
				log.Println("stopped successfully")
				return
			}
		case err := <-errCh:
			log.Println("in start:", err)
		}
	}
}
