package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/getsentry/sentry-go"
	"github.com/orbstack/macvirt/macvmgr/buildid"
	"github.com/orbstack/macvirt/macvmgr/conf"
	"github.com/orbstack/macvirt/macvmgr/conf/appid"
	"github.com/orbstack/macvirt/macvmgr/conf/appver"
	"github.com/orbstack/macvirt/macvmgr/conf/coredir"
	"github.com/orbstack/macvirt/macvmgr/conf/nfsmnt"
	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/macvmgr/drm"
	"github.com/orbstack/macvirt/macvmgr/drm/killswitch"
	"github.com/orbstack/macvirt/macvmgr/flock"
	"github.com/orbstack/macvirt/macvmgr/fsnotify"
	"github.com/orbstack/macvirt/macvmgr/logutil"
	"github.com/orbstack/macvirt/macvmgr/osver"
	"github.com/orbstack/macvirt/macvmgr/util"
	"github.com/orbstack/macvirt/macvmgr/vclient"
	"github.com/orbstack/macvirt/macvmgr/vmclient"
	"github.com/orbstack/macvirt/macvmgr/vmconfig"
	"github.com/orbstack/macvirt/macvmgr/vnet"
	"github.com/orbstack/macvirt/macvmgr/vnet/netconf"
	"github.com/orbstack/macvirt/macvmgr/vnet/services"
	"github.com/orbstack/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/orbstack/macvirt/macvmgr/vzf"
	"github.com/orbstack/macvirt/scon/sclient"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	useStdioConsole = false
	useNat          = false

	handoffWaitLockTimeout = 10 * time.Second
	gracefulStopTimeout    = 15 * time.Second
	sentryShutdownTimeout  = 2 * time.Second
)

type StopType int

const (
	StopForce StopType = iota
	StopGraceful
)

var (
	// host -> guest
	optionalForwards = map[string]string{
		// public SSH
		"tcp:127.0.0.1:" + str(ports.HostSconSSHPublic): "tcp:" + str(ports.GuestSconSSHPublic),
		"tcp:[::1]:" + str(ports.HostSconSSHPublic):     "tcp:" + str(ports.GuestSconSSHPublic),
	}
	essentialForwards = map[string]string{
		// for Swift
		"tcp:127.0.0.1:" + str(ports.HostSconRPC): "tcp:" + str(ports.GuestScon),
		"tcp:[::1]:" + str(ports.HostSconRPC):     "tcp:" + str(ports.GuestScon),
		// unix sockets
		"unix:" + conf.DockerSocket():  "tcp:" + str(ports.GuestDocker),
		"unix:" + conf.SconSSHSocket(): "tcp:" + str(ports.GuestSconSSH),
		"unix:" + conf.SconRPCSocket(): "tcp:" + str(ports.GuestScon),
		// NFS is special, handled below
	}
)

func init() {
	if conf.Debug() {
		optionalForwards["tcp:127.0.0.1:"+str(ports.HostDebugSSH)] = "tcp:" + str(ports.GuestDebugSSH)
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

func extractSparse(file io.ReadCloser) {
	defer file.Close()

	target := conf.DataDir()
	// Go archive/tar doesn't fully support sparse. bsdtar does.
	// apparently some people get not found in PATH so we use the full path
	cmd := exec.Command("/usr/bin/bsdtar", "-xf", "-", "-C", target)
	cmd.Stdin = file
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	check(err)
}

type BytesReadCloser struct {
	*bytes.Reader
}

func (r *BytesReadCloser) Close() error {
	return nil
}

func streamObfAssetFile(name string) io.ReadCloser {
	path := conf.GetAssetFile(name)
	file, err := os.Open(path)
	if err == nil {
		return file
	} else {
		// try obfuscated file
		b64, err := os.ReadFile(path + ".b64")
		check(err)

		// decode base64
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b64)))
		check(err)

		// return reader
		return &BytesReadCloser{bytes.NewReader(decoded)}
	}
}

func setupDockerContext() error {
	// use our builtin docker client so it always works
	dockerBin := conf.FindXbin("docker")

	// create context
	logrus.Debug("creating Docker context")
	_, err := util.Run(dockerBin, "context", "create", appid.AppName, "--description", appid.UserAppName, "--docker", "host=unix://"+conf.DockerSocket())
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// update context if it already exists
			// path can change if username or home dir changes
			_, err = util.Run(dockerBin, "context", "update", appid.AppName, "--docker", "host=unix://"+conf.DockerSocket())
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// use context
	_, err = util.Run(dockerBin, "context", "use", appid.AppName)
	if err != nil {
		return err
	}

	return nil
}

func tryForceStop(vm *vzf.Machine) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("force stop panic: %v", r)
		}
	}()

	err = vm.Stop()
	return
}

func tryGracefulStop(vm *vzf.Machine, vc *vclient.VClient) (err error) {
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

		err = sclient.ShutdownVM()
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

func migrateStateV1ToV2(state *vmconfig.VmgrState) error {
	logrus.WithFields(logrus.Fields{
		"from": "1",
		"to":   "2",
	}).Info("migrating state")

	// check for ~/Linux
	linuxDir := conf.HomeDir() + "/Linux"
	if _, err := os.Stat(linuxDir); err == nil {
		// unmount if it's mounted
		if nfsmnt.IsMountpoint(linuxDir) {
			err = nfsmnt.UnmountNfs()
			if err != nil {
				return err
			}
		}

		// unlink
		err = os.Remove(linuxDir)
		if err != nil {
			// if permission denied, that means dir is not empty
			// leave it alone
			if errors.Is(err, os.ErrPermission) {
				return nil
			}

			return err
		}

		// replace with symlink
		err = os.Symlink(coredir.NfsMountpoint(), linuxDir)
		if err != nil {
			return err
		}
	}

	// nothing to do if ~/Linux doesn't exist
	return nil
}

func migrateState() error {
	old := vmconfig.GetState()
	logrus.Debug("old state: ", old)

	// TODO: future versions need transactional updates
	err := vmconfig.UpdateState(func(state *vmconfig.VmgrState) error {
		ver := state.Version

		// v1: initial version up to 0.4.0

		// v2: 0.4.1 - moved nfs mount from ~/Linux to ~/OrbStack
		if ver == 1 {
			err := migrateStateV1ToV2(state)
			if err != nil {
				return err
			}

			ver = 2
		}

		ver = vmconfig.CurrentVersion
		state.Version = ver

		return nil
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
	logPrefix := color.New(color.FgGreen, color.Bold).Sprint("🌲 vmgr | ")
	logrus.SetFormatter(logutil.NewPrefixFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
	}, logPrefix))

	if !conf.Debug() {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:     "https://8e78517a949a4070a56b23fc1f7b8184@o120089.ingest.sentry.io/4504665519554560",
			Release: appver.Get().Short,
		})
		if err != nil {
			logrus.WithError(err).Error("failed to init Sentry")
		}
		defer sentry.Flush(sentryShutdownTimeout)
	}
	// sentry.Recover() suppresses panic
	defer func() {
		if err := recover(); err != nil {
			hub := sentry.CurrentHub()
			hub.Recover(err)

			panic(err)
		}
	}()

	if !osver.IsAtLeast("v12.3") {
		logrus.Fatal("macOS too old - min 12.3")
	}

	// done signal for shutdown process
	// must close this after all cleanup so next start works (incl. closing listeners)
	doneCh := make(chan struct{})
	defer close(doneCh)

	// parse args
	var buildID string
	var isLaunchd bool
	var waitLock bool
	flag.StringVar(&buildID, "build-id", "", "")
	flag.BoolVar(&isLaunchd, "launchd", false, "")
	flag.BoolVar(&waitLock, "handoff", false, "")
	if len(os.Args) > 2 {
		err := flag.CommandLine.Parse(os.Args[2:])
		check(err)
	}

	// ensure it's not running
	if vmclient.IsRunning() {
		logrus.Fatal("vmgr is already running (socket)")
	}

	// take the lock
	lockFile, err := flock.Open(conf.VmgrLockFile())
	check(err)
	if waitLock {
		// wait lock for spawn-daemon handoff
		_, err = util.WithTimeout(func() (struct{}, error) {
			return struct{}{}, flock.WaitLock(lockFile)
		}, handoffWaitLockTimeout)
		if err != nil {
			logrus.Fatal("vmgr is already running (wait lock): ", err)
		}
	} else {
		err = flock.Lock(lockFile)
		if err != nil {
			logrus.Fatal("vmgr is already running (lock): ", err)
		}
	}
	// for max safety, we never release flock. it'll be released on process exit
	// so keep fd open
	defer runtime.KeepAlive(lockFile)
	/*
		defer func() {
			err := flock.Unlock(lockFile)
			if err != nil {
				logrus.WithError(err).Error("failed to unlock")
			}
		}()
	*/

	// remove everything in run, sockets and pid
	os.RemoveAll(conf.RunDir())
	// then recreate because RunDir only ensures once
	err = os.MkdirAll(conf.RunDir(), 0755)
	check(err)

	// write build ID
	if buildID == "" {
		buildID, err = buildid.CalculateCurrent()
		check(err)
	}
	err = os.WriteFile(conf.VmgrTimestampFile(), []byte(buildID), 0644)
	check(err)

	// everything is set up for spawn-daemon to work properly (build id and pid)
	// now notify GUI that we've started
	vzf.SwextIpcNotifyStarted()

	// killswitch
	err = killswitch.Check()
	if err != nil {
		logrus.Fatal("This beta version has expired. Please update to the latest version: https://orbstack.dev/download")
		panic(err)
	}
	stopCh := make(chan StopType, 1)
	killswitch.Watch(func(err error) {
		logrus.WithError(err).Error("build expired")
		stopCh <- StopGraceful

		go func() {
			time.Sleep(drm.FailStopTimeout)
			os.Exit(1)
		}()
	})

	// Rosetta check
	err = verifyRosetta()
	if err != nil {
		logrus.Fatal(err)
	}

	// state migration
	err = migrateState()
	check(err)

	if _, err := os.Stat(conf.DataImage()); errors.Is(err, os.ErrNotExist) {
		logrus.Info("initializing data")

		// check for apfs
		err := verifyAPFS()
		if err != nil {
			if errors.Is(err, unix.ENOTSUP) {
				logrus.Fatal("Data storage location must be formatted as APFS.")
			} else {
				logrus.WithError(err).Fatal("Failed to check for APFS")
			}
		}
		extractSparse(streamObfAssetFile("data.img.tar"))
	}
	// always overwrite swap - doesn't need persistence
	extractSparse(streamObfAssetFile("swap.img.tar"))

	// remove legacy logs
	_ = os.Remove(conf.ConsoleLog())

	consoleMode := ConsoleLog
	if useStdioConsole {
		consoleMode = ConsoleStdio
	}

	logrus.Info("configuring VM")
	vnetwork, vm := CreateVm(&VmParams{
		Cpus: vmconfig.Get().CPU,
		// default memory algo = 1/3 of host memory, max 10 GB
		Memory: vmconfig.Get().MemoryMiB,
		Kernel: conf.GetAssetFile("kernel"),
		// this one uses gvproxy ssh
		Console:            consoleMode,
		DiskRootfs:         conf.GetAssetFile("rootfs.img"),
		DiskData:           conf.DataImage(),
		DiskSwap:           conf.SwapImage(),
		NetworkVnet:        true,
		NetworkNat:         useNat,
		NetworkHostBridges: 2, // machine + VlanRouter
		MacAddressPrefix:   netconf.GuestMACPrefix,
		Balloon:            true,
		Rng:                true,
		Vsock:              true,
		Virtiofs:           true,
		Rosetta:            vmconfig.Get().Rosetta,
		Sound:              false,

		StopCh: stopCh,
	})
	defer vnetwork.Close()
	// close in case we need to release disk flock for next start
	defer vm.Close()

	// prepare to run async startup tasks
	var startWg sync.WaitGroup
	runAsyncInitTask := func(what string, fn func() error) {
		startWg.Add(1)

		go func() {
			defer startWg.Done()

			err := fn()
			if err != nil {
				logrus.WithError(err).Error(what + " failed")
			}
		}()
	}

	// load proxy settings and proxy password (keychain prompt)
	runAsyncInitTask("proxy settings", vnetwork.Proxy.Refresh)

	// create scon machines host network bridge
	go runOne("host bridge route monitor", vnetwork.MonitorHostBridgeRoutes)
	runAsyncInitTask("host bridge", vnetwork.CreateSconMachineHostBridge)

	// Start DRM
	drmClient := drm.Client()
	// set network
	drmClient.SetVnet(vnetwork)
	go func() {
		ch := drmClient.FailChan()
		for {
			select {
			case <-ch:
				logrus.Error("fail - shutdown")
				stopCh <- StopGraceful

				go func() {
					time.Sleep(drm.FailStopTimeout)
					logrus.Error("fail - force shutdown")
					os.Exit(1)
				}()
				return
			case <-doneCh:
				return
			}
		}
	}()

	// Services
	hcServer := services.StartNetServices(vnetwork)

	// VM control server client
	vc, err := vclient.NewWithNetwork(vnetwork, vm, hcServer)
	check(err)
	defer vc.Close()
	err = vc.StartBackground()
	check(err)

	// fsnotifier
	fsNotifier, err := fsnotify.NewVmNotifier(vnetwork)
	check(err)
	defer fsNotifier.Close()
	hcServer.FsNotifier = fsNotifier
	go runOne("fsnotify proxy", fsNotifier.Run)

	if useStdioConsole {
		fd := int(os.Stdin.Fd())
		state, err := term.MakeRaw(fd)
		check(err)
		defer term.Restore(fd, state)
	}

	// Monitor state changes
	stateChan := vm.StateChan()

	logrus.Info("starting VM")
	err = vm.Start()
	check(err)

	go runOne("data watcher", func() error {
		return WatchCriticalFiles(stopCh)
	})

	// Listen for signals
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

	// Start VM control server for Swift
	logrus.Info("starting host services")
	controlServer := VmControlServer{
		vm:           vm,
		vc:           vc,
		doneCh:       doneCh,
		stopCh:       stopCh,
		dockerClient: makeDockerClient(),
		drm:          drm.Client(),

		setupEnvChan: nil,
		setupReady:   syncx.NewCondBool(),
	}
	vmcontrolCleanup, err := controlServer.Serve()
	check(err)
	defer vmcontrolCleanup()

	// Host forwards (setup vsock)
	vnetwork.VsockDialer = func(port uint32) (net.Conn, error) {
		conn, err := vm.ConnectVsock(port)
		if err != nil {
			return nil, err
		}

		return conn, nil
	}
	for fromSpec, toSpec := range essentialForwards {
		spec := vnet.ForwardSpec{Host: fromSpec, Guest: toSpec}
		_, err := vnetwork.StartForward(spec)
		if err != nil {
			logrus.WithError(err).WithField("spec", spec).Fatal("host forward failed")
		}
	}
	for fromSpec, toSpec := range optionalForwards {
		spec := vnet.ForwardSpec{Host: fromSpec, Guest: toSpec}
		_, err := vnetwork.StartForward(spec)
		if err != nil {
			logrus.WithError(err).WithField("spec", spec).Error("host forward failed")
		}
	}

	// special NFS forward
	// vsock is slightly faster, esp. for small files (because latency)
	nfsFwdSpec := vnet.ForwardSpec{
		// dynamically assigned port
		Host:  "tcp:127.0.0.1:0",
		Guest: "vsock:" + str(ports.GuestNFS),
	}
	nfsFwd, err := vnetwork.StartForward(nfsFwdSpec)
	if err != nil {
		logrus.WithError(err).Fatal("host forward failed")
	}
	nfsPort := nfsFwd.(*tcpfwd.StreamVsockHostForward).TcpPort()
	hcServer.NfsPort = nfsPort

	defer os.Remove(conf.DockerSocket())
	defer os.Remove(conf.SconRPCSocket())
	defer os.Remove(conf.SconSSHSocket())

	// Docker context and certs.d
	runAsyncInitTask("Docker context", func() error {
		// PATH for hostssh, DOCKER_CONFIG for docker cli
		// blocking here because docker depends on it
		err := setupEnv()
		if err != nil {
			logrus.WithError(err).Error("failed to set up environment")
		}
		controlServer.setupReady.Set(true)

		err = setupDockerContext()
		if err != nil {
			logrus.WithError(err).Error("failed to set Docker context")
		}

		return nil
	})

	// SSH key and config
	runAsyncInitTask("public SSH setup", setupPublicSSH)

	// Mount NFS
	defer hcServer.InternalUnmountNfs()

	/*
		logrus.Info("waiting for init tasks")
		startWg.Wait()
	*/

	logrus.Info("waiting for VM to start")
	returnCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	vmHasStarted := false
	for {
		select {
		case <-returnCh:
			return
		case stopReq := <-stopCh:
			logrus.Info("stop requested")
			// unmount nfs first
			hcServer.InternalUnmountNfs()

			go func() {
				switch stopReq {
				case StopForce:
					err := tryForceStop(vm)
					if err != nil {
						logrus.WithError(err).Error("VM force stop failed")
						returnCh <- struct{}{}
					}
				case StopGraceful:
					err := tryGracefulStop(vm, vc)
					if err != nil {
						logrus.WithError(err).Error("VM graceful stop failed")
						returnCh <- struct{}{}
					}
				}
			}()

		case newState := <-stateChan:
			switch newState {
			case vzf.MachineStateStarting:
				logrus.Info("[VM] starting")
			case vzf.MachineStateStopping:
				logrus.Info("[VM] stopping")
			case vzf.MachineStateRunning:
				logrus.Info("[VM] started")
				if !vmHasStarted {
					err := controlServer.onStart()
					if err != nil {
						logrus.WithError(err).Error("vmcontrol start hook failed")
					}
				}
				vmHasStarted = true
			case vzf.MachineStateStopped:
				logrus.Info("[VM] stopped")
				err = controlServer.onStop()
				if err != nil {
					logrus.WithError(err).Error("vmcontrol stop hook failed")
					return
				}
				return
			case vzf.MachineStateError:
				logrus.Error("[VM] error")
				return
			case vzf.MachineStatePaused:
				logrus.Debug("[VM] paused")
			case vzf.MachineStateResuming:
				logrus.Debug("[VM] resuming")
			case vzf.MachineStatePausing:
				logrus.Debug("[VM] pausing")
			}
		case err := <-errCh:
			logrus.WithError(err).Error("error received")
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
	case "report-env":
		runReportEnv()
	case "vmgr", "":
		runVmManager()
	default:
		panic("unknown command: " + cmd)
	}
}
