package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmm/conf"
)

const (
	useRouterPair = false
	useConsole    = false
	useNat        = false
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func extractSparse(tarPath string) {
	target := conf.DataDir()
	// Go archive/tar doesn't fully support sparse. bsdtar does.
	cmd := exec.Command("/usr/bin/bsdtar", "-xf", tarPath, "-C", target)
	err := cmd.Run()
	check(err)
}

func main() {
	var netPair1, netPair2 *os.File
	var err error
	if useRouterPair {
		netPair1, netPair2, err = makeUnixDgramPair()
		check(err)
	}

	if _, err := os.Stat(conf.DataImage()); os.IsNotExist(err) {
		extractSparse(conf.GetAssetFile("data.img.tar"))
	}
	if _, err := os.Stat(conf.SwapImage()); os.IsNotExist(err) {
		extractSparse(conf.GetAssetFile("swap.img.tar"))
	}

	config := &VmConfig{
		Cpus:   runtime.NumCPU(),
		Memory: 6144,
		Kernel: conf.GetAssetFile("kernel"),
		// this one uses gvproxy ssh
		Console:          useConsole,
		DiskRootfs:       conf.GetAssetFile("rootfs.img"),
		DiskData:         conf.DataImage(),
		DiskSwap:         conf.SwapImage(),
		NetworkGvnet:     true,
		NetworkNat:       useNat && !useRouterPair,
		NetworkPairFd:    netPair1,
		MacAddressPrefix: "86:6c:f1:2e:9e",
		Balloon:          true,
		Rng:              true,
		Vsock:            true,
		Virtiofs:         true,
		Rosetta:          true,
		Sound:            false,
	}

	vnetwork, vm := CreateVm(config)

	// VM control server client
	vc := vnetwork.VClient
	err = vc.StartBackground()
	check(err)

	if useConsole {
		oldAttr := setRawMode(os.Stdin)
		defer revertRawMode(os.Stdin, oldAttr)
	}

	err = vm.Start()
	check(err)

	var routerVm *vz.VirtualMachine
	if useRouterPair {
		routerVm = StartRouterVm(netPair2)
	}

	/*
		go func() {
			err := runVsockServices(vm.SocketDevices()[0])
			if err != nil {
				log.Println("vsock services error:", err)
			}
		}()
	*/

	// Listen for signals
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGTERM, syscall.SIGINT)

	errCh := make(chan error, 1)

	// Start host control server for Swift
	controlServer := HostControlServer{
		balloon:  vm.MemoryBalloonDevices()[0],
		routerVm: routerVm,
		netPair2: netPair2,
	}
	httpServer, err := controlServer.Serve()
	check(err)
	defer httpServer.Shutdown(context.TODO())

	routerVm = nil

	// Mount NFS
	nfsMounted := false
	go func() {
		vc.WaitForDataReady()
		log.Println("Mounting NFS...")
		err := conf.MountNfs()
		if err != nil {
			// if already mounted, we'll just reuse it
			log.Println("NFS mount error:", err)
			return
		}
		log.Println("NFS mounted")
		nfsMounted = true
	}()
	defer func() {
		if nfsMounted {
			log.Println("Unmounting NFS...")
			err := conf.UnmountNfs()
			if err != nil {
				log.Println("NFS unmount error:", err)
			}
			log.Println("NFS unmounted")
		}
	}()

	for {
		select {
		case <-signalCh:
			log.Println("stop (signal)")
			err := vm.Stop()
			if err != nil {
				log.Println("VM stop error:", err)
				return
			}
		case newState := <-vm.StateChangedNotify():
			if newState == vz.VirtualMachineStateRunning {
				log.Println("VM started")
			}
			if newState == vz.VirtualMachineStateStopped {
				log.Println("VM stopped")
				return
			}
			if newState == vz.VirtualMachineStateError {
				log.Println("VM error")
				return
			}
		case err := <-errCh:
			log.Println("VM start error:", err)
			return
		}
	}
}
