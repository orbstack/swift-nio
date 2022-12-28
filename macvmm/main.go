package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmm/conf"
	"github.com/kdrag0n/macvirt/macvmm/vnet"
	"golang.org/x/sys/unix"
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

func createDockerContext() {
	var errBuf bytes.Buffer
	createCmd := exec.Command("docker", "context", "create", conf.AppName(), "--description", conf.AppNameUser(), "--docker", "host=unix://"+conf.DockerSocket())
	createCmd.Stderr = &errBuf
	err := createCmd.Run()
	if err != nil {
		if strings.Contains(errBuf.String(), "already exists") {
			return
		}
		log.Println("Failed to create Docker context:", err)
	}
}

func setDockerContext() {
	createDockerContext()

	err := exec.Command("docker", "context", "use", conf.AppName()).Run()
	if err != nil {
		log.Println("Failed to set Docker context:", err)
	}
}

func isMountpoint(path string) bool {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if err != nil {
		return false
	}

	var parentStat unix.Stat_t
	err = unix.Stat(path+"/..", &parentStat)
	if err != nil {
		return false
	}

	return stat.Dev != parentStat.Dev
}

func tryStop(vm *vz.VirtualMachine) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("stop panic: %v", r)
		}
	}()

	err = vm.Stop()
	return
}

func main() {
	if err := vz.MacOSAvailable(12); err != nil {
		log.Fatal(err)
	}

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
		vc:       vc,
	}
	httpServer, err := controlServer.Serve()
	check(err)
	defer httpServer.Shutdown(context.TODO())

	// Host forwards (setup vsock)
	vsock := vm.SocketDevices()[0]
	vnetwork.VsockDialer = func(port uint32) (net.Conn, error) {
		conn, err := vsock.Connect(port)
		if err != nil {
			return nil, err
		}

		return conn.RawConn(), nil
	}
	for fromSpec, toSpec := range vnet.HostForwardsToGuest {
		err := vnetwork.StartForward(fromSpec, toSpec)
		if err != nil {
			log.Println("host forward error:", err)
		}
	}

	// Docker context
	go setDockerContext()
	defer os.Remove(conf.DockerSocket())

	// Mount NFS
	nfsMounted := false
	go func() {
		vc.WaitForDataReady()
		log.Println("Mounting NFS...")
		err := conf.MountNfs()
		if err != nil {
			// careful, this could hang
			if isMountpoint(conf.NfsMountpoint()) {
				log.Println("NFS already mounted")
				nfsMounted = true
				return
			}

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
			err := tryStop(vm)
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
