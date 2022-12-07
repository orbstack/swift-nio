package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Code-Hex/vz/v3"
	"github.com/pkg/term/termios"
	"golang.org/x/sys/unix"
)

// https://developer.apple.com/documentation/virtualization/running_linux_in_a_virtual_machine?language=objc#:~:text=Configure%20the%20Serial%20Port%20Device%20for%20Standard%20In%20and%20Out
func setRawMode(f *os.File) {
	var attr unix.Termios

	// Get settings for terminal
	// this still isn't raw mode, still converts ^C
	//termios.Tcgetattr(f.Fd(), &attr)

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
		// userspace
		"vc.data_size=10240",
		"vc.vcontrol_token=test",
		"vc.timezone=America/Los_Angeles",
	}, " ")
	fmt.Println("cmdline", cmdline)

	bootLoader, err := vz.NewLinuxBootLoader(
		"../assets/kernel",
		vz.WithCommandLine(cmdline),
	)
	check(err)
	fmt.Println("bootloader", bootLoader)

	config, err := vz.NewVirtualMachineConfiguration(
		bootLoader,
		8,
		1200*1024*1024,
	)
	check(err)

	// Console
	setRawMode(os.Stdin)
	serialPortAttachment, err := vz.NewFileHandleSerialPortAttachment(os.Stdin, os.Stdout)
	check(err)
	consoleConfig, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialPortAttachment)
	check(err)
	config.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{
		consoleConfig,
	})

	// Network
	natAttachment, err := vz.NewNATNetworkDeviceAttachment()
	check(err)
	networkConfig, err := vz.NewVirtioNetworkDeviceConfiguration(natAttachment)
	check(err)
	config.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{
		networkConfig,
	})
	mac, err := vz.NewRandomLocallyAdministeredMACAddress()
	check(err)
	networkConfig.SetMACAddress(mac)

	// RNG
	entropyConfig, err := vz.NewVirtioEntropyDeviceConfiguration()
	check(err)
	config.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{
		entropyConfig,
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
	memoryBalloonDevice, err := vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration()
	check(err)
	config.SetMemoryBalloonDevicesVirtualMachineConfiguration([]vz.MemoryBalloonDeviceConfiguration{
		memoryBalloonDevice,
	})

	// Vsock
	vsockDevice, err := vz.NewVirtioSocketDeviceConfiguration()
	check(err)
	config.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{
		vsockDevice,
	})
	validated, err := config.Validate()
	check(err)
	if !validated {
		log.Fatal("validation failed", err)
	}

	// Boot!
	vm, err := vz.NewVirtualMachine(config)
	check(err)

	// Listen for signals
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM)

	err = vm.Start()
	check(err)

	errCh := make(chan error, 1)

	for {
		select {
		case <-signalCh:
			result, err := vm.RequestStop()
			if err != nil {
				log.Println("request stop error:", err)
				return
			}
			log.Println("recieved signal", result)
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
