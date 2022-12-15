package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/kdrag0n/vz-macvirt/v3"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	netPair1, _, err := makeUnixDgramPair()
	config := &VmConfig{
		Cpus:             runtime.NumCPU(),
		Memory:           6144,
		Kernel:           "../assets/kernel",
		Console:          true,
		DiskRootfs:       "../assets/rootfs.img",
		DiskData:         "../assets/data.img",
		DiskSwap:         "../assets/swap.img",
		NetworkNat:       true,
		NetworkGvproxy:   true,
		NetworkPairFd:    netPair1,
		MacAddressPrefix: "86:6c:f1:2e:9e",
		Balloon:          true,
		Rng:              true,
		Vsock:            true,
		Virtiofs:         true,
		Rosetta:          true,
		Sound:            false,
	}

	vm := CreateVm(config)

	go func() {
		err := runVsockServices(vm.SocketDevices()[0])
		if err != nil {
			log.Println("vsock services error:", err)
		}
	}()

	oldAttr := setRawMode(os.Stdin)
	defer revertRawMode(os.Stdin, oldAttr)

	err = vm.Start()
	check(err)

	// Listen for signals
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM)

	errCh := make(chan error, 1)

	controlServer := HostControlServer{
		balloon: vm.MemoryBalloonDevices()[0],
	}
	httpServer, err := controlServer.Serve()
	check(err)
	defer httpServer.Shutdown(context.Background())

	/*
		go func() {
			time.Sleep(5 * time.Second)
			conn, err := vm.SocketDevices()[0].Connect(5200)
			if err != nil {
				log.Println("vsock connect error:", err)
				return
			}

			err = benchmarkVsock(conn)
			if err != nil {
				log.Println("vsock benchmark error:", err)
			}
		}()
	*/

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
