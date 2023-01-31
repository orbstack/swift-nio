package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mem"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sys/unix"
)

const (
	useRouterPair   = false
	useStdioConsole = false
	useNat          = false

	nfsMountTries = 10
	nfsMountDelay = 500 * time.Millisecond

	defaultMemoryLimit = 8 * 1024 * 1024 * 1024 // 8 GiB
)

var (
	// host -> guest
	hostForwardsToGuest = map[string]string{
		// "tcp:127.0.0.1:" + str(ports.HostNFS): "tcp:" + str(ports.GuestNFS),
		"tcp:127.0.0.1:" + str(ports.HostNFS):           "vsock:" + str(ports.GuestNFS),
		"tcp:127.0.0.1:" + str(ports.HostSconSSHPublic): "tcp:" + str(ports.GuestSconSSHPublic),
		"tcp:[::1]:" + str(ports.HostSconSSHPublic):     "tcp:" + str(ports.GuestSconSSHPublic),
		"unix:" + conf.DockerSocket():                   "tcp:" + str(ports.GuestDocker),
		"unix:" + conf.SconSSHSocket():                  "tcp:" + str(ports.GuestSconSSH),
		"unix:" + conf.SconRPCSocket():                  "tcp:" + str(ports.GuestScon),
	}
)

func init() {
	if conf.Debug() {
		hostForwardsToGuest["tcp:127.0.0.1:"+str(ports.HostSSH)] = "tcp:" + str(ports.GuestDebugSSH)
	}
}

func str(port int) string {
	return strconv.Itoa(port)
}

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
	createCmd := exec.Command("docker", "context", "create", appid.AppName, "--description", appid.UserAppName, "--docker", "host=unix://"+conf.DockerSocket())
	createCmd.Stderr = &errBuf
	err := createCmd.Run()
	if err != nil {
		if strings.Contains(errBuf.String(), "already exists") {
			return
		}
		logrus.Error("Failed to create Docker context:", err)
	}
}

func setDockerContext() {
	createDockerContext()

	err := exec.Command("docker", "context", "use", appid.AppName).Run()
	if err != nil {
		logrus.Error("Failed to set Docker context:", err)
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

func calcMemory() uint64 {
	hostMem := mem.PhysicalMemory()
	if hostMem > defaultMemoryLimit {
		return defaultMemoryLimit
	}
	return hostMem / 3
}

func writePidFile() {
	pidFile, err := os.Create(conf.VmgrPidFile())
	check(err)
	defer pidFile.Close()

	_, err = pidFile.WriteString(strconv.Itoa(os.Getpid()))
	check(err)
}

func runVmManager() {
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "01-02 15:04:05",
		})
	}

	if err := vz.MacOSAvailable(12.6); err != nil {
		logrus.Fatal("macOS too old", err)
	}

	// start over with the sockets
	os.RemoveAll(conf.RunDir())

	// write PID file
	writePidFile()
	defer os.Remove(conf.VmgrPidFile())

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

	consoleMode := ConsoleLog
	if useStdioConsole {
		consoleMode = ConsoleStdio
	}
	config := &VmConfig{
		Cpus: runtime.NumCPU(),
		// default memory algo = 1/3 of host memory, max 10 GB
		Memory: calcMemory() / 1024 / 1024,
		Kernel: conf.GetAssetFile("kernel"),
		// this one uses gvproxy ssh
		Console:          consoleMode,
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

	// Services
	services.StartNetServices(vnetwork)

	// VM control server client
	vc, err := vclient.NewWithNetwork(vnetwork)
	check(err)
	err = vc.StartBackground()
	check(err)

	if useStdioConsole {
		fd := int(os.Stdin.Fd())
		state, err := terminal.MakeRaw(fd)
		check(err)
		defer terminal.Restore(fd, state)
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
	signal.Notify(signalCh, unix.SIGTERM, unix.SIGINT, unix.SIGQUIT)

	errCh := make(chan error, 1)

	// Start VM control server for Swift
	controlServer := VmControlServer{
		balloon:  vm.MemoryBalloonDevices()[0],
		routerVm: routerVm,
		netPair2: netPair2,
		vc:       vc,
	}
	unixListener, err := controlServer.Serve()
	check(err)
	defer unixListener.Close()

	// Host forwards (setup vsock)
	vsock := vm.SocketDevices()[0]
	vnetwork.VsockDialer = func(port uint32) (net.Conn, error) {
		conn, err := vsock.Connect(port)
		if err != nil {
			return nil, err
		}

		return conn.RawConn(), nil
	}
	for fromSpec, toSpec := range hostForwardsToGuest {
		spec := vnet.ForwardSpec{Host: fromSpec, Guest: toSpec}
		err := vnetwork.StartForward(spec)
		if err != nil {
			logrus.WithError(err).WithField("spec", spec).Fatal("host forward failed")
		}
	}
	defer os.Remove(conf.DockerSocket())
	defer os.Remove(conf.SconRPCSocket())
	defer os.Remove(conf.SconSSHSocket())

	// Docker context
	go setDockerContext()

	// Mount NFS
	nfsMounted := false
	go func() {
		vc.WaitForDataReady()

		// vsock fails immediately unlike tcp dialing, so try 5 times
		for i := 0; i < nfsMountTries; i++ {
			logrus.Info("Mounting NFS...")
			err := conf.MountNfs()
			if err != nil {
				// if already mounted, we'll just reuse it
				// careful, this could hang
				if isMountpoint(conf.NfsMountpoint()) {
					logrus.Info("NFS already mounted")
					nfsMounted = true
					return
				}

				logrus.Error("NFS mount error: ", err)
				time.Sleep(nfsMountDelay)
				continue
			}

			logrus.Info("NFS mounted")
			nfsMounted = true
			break
		}
	}()
	unmountNfs := func() {
		if nfsMounted {
			logrus.Info("Unmounting NFS...")
			err := conf.UnmountNfs()
			if err != nil {
				logrus.Error("NFS unmount error:", err)
			}
			logrus.Info("NFS unmounted")
			nfsMounted = false
		}
	}
	defer unmountNfs()

	for {
		select {
		case <-signalCh:
			logrus.Info("stop (signal)")
			// unmount nfs first
			unmountNfs()
			err := tryStop(vm)
			if err != nil {
				logrus.Info("VM stop error:", err)
				return
			}
		case newState := <-stateChan:
			if newState == vz.VirtualMachineStateRunning {
				logrus.Info("VM started")
			}
			if newState == vz.VirtualMachineStateStopped {
				logrus.Info("VM stopped")
				return
			}
			if newState == vz.VirtualMachineStateError {
				logrus.Error("VM error")
				return
			}
		case err := <-errCh:
			logrus.Error("VM start error:", err)
			return
		}
	}
}

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "spawn-daemon":
		runSpawnDaemon()
	case "":
		runVmManager()
	default:
		panic("unknown command: " + cmd)
	}
}
