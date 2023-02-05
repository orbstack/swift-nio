package main

import (
	"bytes"
	"errors"
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
	"github.com/gofrs/flock"
	"github.com/kdrag0n/macvirt/macvmgr/buildid"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services"
	"github.com/kdrag0n/macvirt/scon/sclient"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	useStdioConsole = false
	useNat          = false

	nfsMountTries = 10
	nfsMountDelay = 500 * time.Millisecond

	gracefulStopTimeout = 15 * time.Second
)

type StopType int

const (
	StopForce StopType = iota
	StopGraceful
)

var (
	// host -> guest
	hostForwardsToGuest = map[string]string{
		// "tcp:127.0.0.1:" + str(ports.HostNFS): "tcp:" + str(ports.GuestNFS),
		"tcp:127.0.0.1:" + str(ports.HostNFS):           "vsock:" + str(ports.GuestNFS),
		"tcp:127.0.0.1:" + str(ports.HostSconSSHPublic): "tcp:" + str(ports.GuestSconSSHPublic),
		"tcp:[::1]:" + str(ports.HostSconSSHPublic):     "tcp:" + str(ports.GuestSconSSHPublic),
		"tcp:127.0.0.1:" + str(ports.HostSconRPC):       "tcp:" + str(ports.GuestScon),
		"tcp:[::1]:" + str(ports.HostSconRPC):           "tcp:" + str(ports.GuestScon),
		"unix:" + conf.DockerSocket():                   "tcp:" + str(ports.GuestDocker),
		"unix:" + conf.SconSSHSocket():                  "tcp:" + str(ports.GuestSconSSH),
		"unix:" + conf.SconRPCSocket():                  "tcp:" + str(ports.GuestScon),
	}
)

func init() {
	if conf.Debug() {
		hostForwardsToGuest["tcp:127.0.0.1:"+str(ports.HostDebugSSH)] = "tcp:" + str(ports.GuestDebugSSH)
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

func tryForceStop(vm *vz.VirtualMachine) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("force stop panic: %v", r)
		}
	}()

	err = vm.Stop()
	return
}

func tryGracefulStop(vm *vz.VirtualMachine, vc *vclient.VClient) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("graceful stop panic: %v", r)
		}
	}()

	go func() {
		//TODO signal via channel close and select on TimeAfter
		time.Sleep(gracefulStopTimeout)
		logrus.Error("graceful stop timed out, forcing")

		// assume that main goroutine would've exited by now, so program would've exited
		// safe because onStop hook will never be set for graceful stop
		err := tryForceStop(vm)
		if err != nil {
			logrus.WithError(err).Error("failed to force stop VM after graceful stop timeout")
		}
	}()

	// 1. try scon
	logrus.Debug("trying to stop VM via scon")
	sclient, err := sclient.New("unix", conf.SconRPCSocket())
	if err == nil {
		defer sclient.Close()

		err = sclient.StopServerVM()
		if err == nil {
			return
		} else {
			logrus.WithError(err).Error("failed to stop via scon")
		}
	} else {
		logrus.WithError(err).Error("failed to stop via scon")
	}

	// 2. try vcontrol
	logrus.Debug("trying to stop VM via vcontrol")
	err = vc.Shutdown()
	if err == nil {
		return
	} else {
		logrus.WithError(err).Error("failed to stop via vcontrol")
	}

	// 3. try vz
	/*
		logrus.Debug("trying to stop VM via vz")
		stopped, err := vm.RequestStop()
		if stopped && err == nil {
			return
		} else {
			logrus.WithError(err).Error("failed to stop via vz")
		}*/

	// 4. try force
	logrus.Debug("trying to stop VM via force vz")
	err = tryForceStop(vm)
	if err == nil {
		return
	} else {
		logrus.WithError(err).Error("failed to stop via force vz")
	}

	return
}

func writePidFile() {
	pidFile, err := os.Create(conf.VmgrPidFile())
	check(err)
	defer pidFile.Close()

	_, err = pidFile.WriteString(strconv.Itoa(os.Getpid()))
	check(err)
}

func migrateState() error {
	old := vmconfig.GetState()
	logrus.Debug("old state: ", old)

	err := vmconfig.UpdateState(func(state *vmconfig.VmgrState) {
		state.Version = vmconfig.CurrentVersion
	})
	if err != nil {
		return err
	}

	return nil
}

func runOne(what string, fn func() error) {
	err := fn()
	if err != nil {
		logrus.WithError(err).Error(what + " failed")
	}
}

func runVmManager() {
	if conf.Debug() {
		logrus.SetLevel(logrus.DebugLevel)
	}
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
	})

	if err := vz.MacOSAvailable(12.6); err != nil {
		logrus.Fatal("macOS too old", err)
	}

	// ensure it's not running
	if vmclient.IsRunning() {
		logrus.Fatal("vmgr is already running (socket)")
	}

	// take the lock
	lockFile := flock.New(conf.VmgrLockFile())
	locked, err := lockFile.TryLock()
	if err != nil {
		logrus.Fatal("failed to take lock:", err)
	}
	if !locked {
		logrus.Fatal("vmgr is already running (lock)")
	}
	defer lockFile.Unlock()

	// remove everything in run, sockets and pid
	os.RemoveAll(conf.RunDir())

	// write PID file
	writePidFile()
	defer os.Remove(conf.VmgrPidFile())

	// write build ID
	var buildID string
	if len(os.Args) > 2 {
		buildID = os.Args[2]
	} else {
		buildID, err = buildid.CalculateCurrent()
		check(err)
	}
	err = os.WriteFile(conf.VmgrVersionFile(), []byte(buildID), 0644)
	check(err)

	// state migration
	err = migrateState()
	check(err)

	doneCh := make(chan struct{})
	defer close(doneCh)

	if _, err := os.Stat(conf.DataImage()); errors.Is(err, os.ErrNotExist) {
		// check for apfs
		err := verifyAPFS()
		if err != nil {
			logrus.Fatal("APFS is required")
		}
		extractSparse(conf.GetAssetFile("data.img.tar"))
	}
	if _, err := os.Stat(conf.SwapImage()); errors.Is(err, os.ErrNotExist) {
		extractSparse(conf.GetAssetFile("swap.img.tar"))
	}

	consoleMode := ConsoleLog
	if useStdioConsole {
		consoleMode = ConsoleStdio
	}
	params := &VmParams{
		Cpus: runtime.NumCPU(),
		// default memory algo = 1/3 of host memory, max 10 GB
		Memory: vmconfig.Get().MemoryMiB,
		Kernel: conf.GetAssetFile("kernel"),
		// this one uses gvproxy ssh
		Console:          consoleMode,
		DiskRootfs:       conf.GetAssetFile("rootfs.img"),
		DiskData:         conf.DataImage(),
		DiskSwap:         conf.SwapImage(),
		NetworkGvnet:     true,
		NetworkNat:       useNat,
		MacAddressPrefix: "86:6c:f1:2e:9e",
		Balloon:          true,
		Rng:              true,
		Vsock:            true,
		Virtiofs:         true,
		Rosetta:          true,
		Sound:            false,
	}

	vnetwork, vm := CreateVm(params)

	// Services
	services.StartNetServices(vnetwork)

	// VM control server client
	vc, err := vclient.NewWithNetwork(vnetwork)
	check(err)
	defer vc.Close()
	err = vc.StartBackground()
	check(err)

	if useStdioConsole {
		fd := int(os.Stdin.Fd())
		state, err := term.MakeRaw(fd)
		check(err)
		defer term.Restore(fd, state)
	}

	err = vm.Start()
	check(err)

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
	stopCh := make(chan StopType, 1)
	go func() {
		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh, unix.SIGTERM, unix.SIGINT, unix.SIGQUIT)

		sigints := 0
		for {
			sig := <-signalCh
			if sig == unix.SIGINT {
				sigints++
			} else {
				sigints = 0
			}

			if sigints >= 2 {
				// two SIGINT = force stop
				logrus.Info("Received SIGINT twice, forcing stop")
				stopCh <- StopForce
			} else {
				logrus.Info("Received signal, requesting stop")
				stopCh <- StopGraceful
			}
		}
	}()

	errCh := make(chan error, 1)

	// Start VM control server for Swift
	controlServer := VmControlServer{
		balloon: vm.MemoryBalloonDevices()[0],
		vm:      vm,
		vc:      vc,
		doneCh:  doneCh,
		stopCh:  stopCh,
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

	// SSH key and config
	go runOne("public SSH setup", setupPublicSSH)

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

				logrus.WithError(err).Error("NFS mount failed")
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
				logrus.WithError(err).Error("NFS unmount failed")
			}
			logrus.Info("NFS unmounted")
			nfsMounted = false
		}
	}
	defer unmountNfs()

	for {
		select {
		case stopReq := <-stopCh:
			logrus.Info("stop requested")
			// unmount nfs first
			unmountNfs()

			go func() {
				switch stopReq {
				case StopForce:
					err := tryForceStop(vm)
					if err != nil {
						logrus.WithError(err).Error("VM force stop failed")
						return
					}
				case StopGraceful:
					err := tryGracefulStop(vm, vc)
					if err != nil {
						logrus.WithError(err).Error("VM graceful stop failed")
						return
					}
				}
			}()

		case newState := <-stateChan:
			if newState == vz.VirtualMachineStateRunning {
				logrus.Info("VM started")
			}
			if newState == vz.VirtualMachineStateStopped {
				logrus.Info("VM stopped")
				err = controlServer.onStop()
				if err != nil {
					logrus.WithError(err).Error("vmcontrol stop hook failed")
					return
				}
				return
			}
			if newState == vz.VirtualMachineStateError {
				logrus.Error("VM error")
				return
			}
		case err := <-errCh:
			logrus.WithError(err).Error("VM error")
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
	case "ssh-proxy-fdpass":
		runSshProxyFdpass()
	case "vmgr", "":
		runVmManager()
	default:
		panic("unknown command: " + cmd)
	}
}
