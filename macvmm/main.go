package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmm/conf"
	"github.com/kdrag0n/macvirt/macvmm/vnet"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"
)

const (
	useRouterPair = false
	useConsole    = false
	useNat        = false

	nfsMountTries = 10
	nfsMountDelay = 500 * time.Millisecond
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
		klog.Error("Failed to create Docker context:", err)
	}
}

func setDockerContext() {
	createDockerContext()

	err := exec.Command("docker", "context", "use", conf.AppName()).Run()
	if err != nil {
		klog.Error("Failed to set Docker context:", err)
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
	if conf.Debug() {
		klog.InitFlags(nil)
	}

	if err := vz.MacOSAvailable(12.6); err != nil {
		klog.Fatal("macOS too old", err)
	}

	var netPair1, netPair2 *os.File
	if useRouterPair {
		file1, fd2, err := vnet.NewUnixgramPair()
		check(err)
		netPair1 = file1
		netPair2 = os.NewFile(uintptr(fd2), "socketpair1")
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
	err := vc.StartBackground()
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

	// Monitor state changes even if observer panics
	stateChan := make(chan vz.VirtualMachineState)
	go func() {
		vmChan := vm.StateChangedNotify()
		for {
			select {
			case state := <-vmChan:
				stateChan <- state
			case state := <-vz.GlobalStateChan:
				stateChan <- state
			}
		}
	}()

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
			klog.Error("host forward failed", err)
		}
	}

	// Docker context
	go setDockerContext()
	defer os.Remove(conf.DockerSocket())

	// Mount NFS
	nfsMounted := false
	go func() {
		vc.WaitForDataReady()

		// vsock fails immediately unlike tcp dialing, so try 5 times
		for i := 0; i < nfsMountTries; i++ {
			klog.Info("Mounting NFS...")
			err := conf.MountNfs()
			if err != nil {
				// if already mounted, we'll just reuse it
				// careful, this could hang
				if isMountpoint(conf.NfsMountpoint()) {
					klog.Info("NFS already mounted")
					nfsMounted = true
					return
				}

				klog.Error("NFS mount error:", err)
				time.Sleep(nfsMountDelay)
				continue
			}

			klog.Info("NFS mounted")
			nfsMounted = true
			break
		}
	}()
	unmountNfs := func() {
		if nfsMounted {
			klog.Info("Unmounting NFS...")
			err := conf.UnmountNfs()
			if err != nil {
				klog.Error("NFS unmount error:", err)
			}
			klog.Info("NFS unmounted")
			nfsMounted = false
		}
	}
	defer unmountNfs()

	for {
		select {
		case <-signalCh:
			klog.Info("stop (signal)")
			// unmount nfs first
			unmountNfs()
			err := tryStop(vm)
			if err != nil {
				klog.Info("VM stop error:", err)
				return
			}
		case newState := <-stateChan:
			if newState == vz.VirtualMachineStateRunning {
				klog.Info("VM started")
			}
			if newState == vz.VirtualMachineStateStopped {
				klog.Info("VM stopped")
				return
			}
			if newState == vz.VirtualMachineStateError {
				klog.Error("VM error")
				return
			}
		case err := <-errCh:
			klog.Error("VM start error:", err)
			return
		}
	}
}
