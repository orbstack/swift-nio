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
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/getsentry/sentry-go"
	"github.com/orbstack/macvirt/vmgr/buildid"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/conf/nfsmnt"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/conf/sentryconf"
	"github.com/orbstack/macvirt/vmgr/drm"
	"github.com/orbstack/macvirt/vmgr/drm/drmid"
	"github.com/orbstack/macvirt/vmgr/drm/killswitch"
	_ "github.com/orbstack/macvirt/vmgr/earlyinit"
	"github.com/orbstack/macvirt/vmgr/flock"
	"github.com/orbstack/macvirt/vmgr/fsnotify"
	"github.com/orbstack/macvirt/vmgr/logutil"
	"github.com/orbstack/macvirt/vmgr/osver"
	"github.com/orbstack/macvirt/vmgr/rsvm"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/util/errorx"
	"github.com/orbstack/macvirt/vmgr/util/pspawn"
	"github.com/orbstack/macvirt/vmgr/vclient"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/services"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	useStdioConsole = false
	useNat          = false

	handoffWaitLockTimeout = 10 * time.Second // also applies to data.img flock
	gracefulStopTimeout    = 15 * time.Second
	deferredCleanupTimeout = 15 * time.Second // in case of deadlock
	sentryShutdownTimeout  = 2 * time.Second

	// we use a small swap file for emergency OOM cases
	swapSize = 1024 * 1024 * 1024 // 1 GiB

	stopExitCodeBase = 100
)

var errDataPermission = errors.New(`Permission denied while opening data image. This is usually caused by Migration Assistant changing its owner to root. To fix it, run: "sudo chown -R $USER ~/.orbstack/data"`)

// host -> guest
var optionalForwardsLocalhost = map[string]string{
	// public SSH
	"tcp:127.0.0.1:" + str(ports.HostSconSSHPublic): "tcp:" + str(ports.GuestSconSSHPublic),
	"tcp:[::1]:" + str(ports.HostSconSSHPublic):     "tcp:" + str(ports.GuestSconSSHPublic),
}
var optionalForwardsPublic = map[string]string{
	// public SSH
	"tcp:[::]:" + str(ports.HostSconSSHPublic): "tcp:" + str(ports.GuestSconSSHPublic),
}

func init() {
	if conf.Debug() {
		optionalForwardsLocalhost["tcp:127.0.0.1:"+str(ports.HostDebugSSH)] = "tcp:" + str(ports.GuestDebugSSH)
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
	cmd := pspawn.Command("/usr/bin/bsdtar", "-xf", "-", "-C", target)
	cmd.Stdin = file
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// bsdtar: Failed to set default locale
	cmd.Env = append(os.Environ(), "LANG=C")
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

func createEmptySwap() error {
	// 0600 because this may contain sensitive memory data
	path := conf.SwapImage()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w. (%w)", errDataPermission, err)
		} else {
			return err
		}
	}
	defer file.Close()

	// ftruncate to create sparse file
	// TODO: EINTR safety
	err = unix.Ftruncate(int(file.Fd()), swapSize)
	if err != nil {
		return fmt.Errorf("ftruncate swap: %w", err)
	}

	return nil
}

func setupDockerContext() error {
	if !vmconfig.Get().DockerSetContext {
		logrus.Debug("not setting Docker context")
		return nil
	}

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

func tryGracefulStop(vm vmm.Machine, vc *vclient.VClient) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// FIXME: could come from gvisor (vinit call)
			err = fmt.Errorf("graceful stop panic: %v", r)
		}
	}()

	go func() {
		//TODO signal via channel close and select on TimeAfter
		time.Sleep(gracefulStopTimeout)
		logrus.Error("graceful stop timed out, forcing")

		// assume that main goroutine would've exited by now, so program would've exited
		// safe because onStop hook will never be set for graceful stop
		err := vm.ForceStop()
		if err != nil {
			logrus.WithError(err).Error("failed to force stop VM after graceful stop timeout")
		}
	}()

	// 1. vinit
	logrus.Debug("trying to stop VM via vinit")
	err = vc.Shutdown()
	if err == nil {
		//TODO what about conn closed
		return
	} else {
		logrus.WithError(err).Error("failed to stop via vinit")
	}

	// 2. force
	logrus.Debug("trying to stop VM via force vz")
	err = vm.ForceStop()
	if err == nil {
		return
	} else {
		logrus.WithError(err).Error("failed to stop via force vz")
	}

	return
}

func migrateStateV1ToV2() error {
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

func migrateStateV2ToV3() error {
	logrus.WithFields(logrus.Fields{
		"from": "2",
		"to":   "3",
	}).Info("migrating state")

	// old default: set DataAllowBackup to true - IF time machine is enabled
	// otherwise, set to false
	err := vmconfig.Update(func(c *vmconfig.VmConfig) {
		c.DataAllowBackup = util.CheckTimeMachineEnabled()
	})
	if err != nil {
		return err
	}

	return nil
}

func migrateState() error {
	old := vmconfig.GetState()
	logrus.Debug("old state: ", old)

	// TODO: future versions need transactional updates
	err := vmconfig.UpdateState(func(state *vmconfig.VmgrState) error {
		// enforce upper bound on legacy version, to fix v1.6.0-rc1/rc2 canary preventing downgrade due to 2->3 version bump
		if state.LegacyVersion > vmconfig.LastLegacyVersion {
			state.LegacyVersion = vmconfig.LastLegacyVersion
		}

		// migrate version number first
		if state.LegacyVersion != 0 && state.MajorVersion == 0 && state.MinorVersion == 0 {
			state.MajorVersion = 0
			state.MinorVersion = state.LegacyVersion // last = 2 (or 3 in canary)

			// keep LegacyVersion for downgrade support
		}

		major := state.MajorVersion
		minor := state.MinorVersion

		// v1: initial version up to 0.4.0

		// v2: 0.4.1 - moved nfs mount from ~/Linux to ~/OrbStack
		if minor == 1 {
			err := migrateStateV1ToV2()
			if err != nil {
				return err
			}

			minor = 2
		}

		if minor == 2 {
			err := migrateStateV2ToV3()
			if err != nil {
				return err
			}

			minor = 3
		}

		major = vmconfig.CurrentMajorVersion
		minor = vmconfig.CurrentMinorVersion
		state.MajorVersion = major
		state.MinorVersion = minor
		// always write store last legacy version to allow downgrade from new installs
		// also account for major here to prevent major downgrade to older versions that don't understand major/minor versions. (running an old build will delete major/minor fields)
		state.LegacyVersion = vmconfig.LastLegacyVersion + major

		// update current architecture (can migrate w/o changes)
		state.Arch = runtime.GOARCH

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

func flushDisk() error {
	fd, err := unix.Open(conf.DataImage(), unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer unix.Close(fd)

	_, err = unix.FcntlInt(uintptr(fd), unix.F_FULLFSYNC, 0)
	if err != nil {
		return fmt.Errorf("fsync: %w", err)
	}

	return nil
}

// VirtualMachineService uses flock and returns "invalid storage device attachment" if not locked
// since we're under main vmgr flock, it's guaranteed that the lock is free after we get it and unlock it
// must do it before creating VM config, or storage config/attachment is reported as invalid on vm.Start()
// --
// this deals with cases like vmgr force stop + VMM cleaning up a lot of fds before exiting
// TODO: this can be done better with rsvm
func ensureDataLock() error {
	dataImg, err := os.OpenFile(conf.DataImage(), os.O_RDWR, 0644)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w. (%w)", errDataPermission, err)
		} else {
			return err
		}
	}
	defer dataImg.Close()

	err = util.WithTimeout1(func() error {
		return flock.WaitLock(dataImg)
	}, handoffWaitLockTimeout)
	if err != nil {
		return fmt.Errorf("wait data lock: %w", err)
	}

	return nil
}

type StopDeadlockError struct {
	stack string
}

func (e StopDeadlockError) Error() string {
	return "stop deadlock: " + e.stack
}

func filterStacks(str string) string {
	var newStacks []string
	for _, stk := range strings.Split(str, "\n\n") {
		// problem is in host bridge
		if strings.Contains(stk, "/vnet/") && !strings.Contains(stk, "gvisor") && !strings.Contains(stk, "gonet") && !strings.Contains(stk, "dglink") {
			newStacks = append(newStacks, stk)
		}
	}
	return strings.Join(newStacks, "\n\n")
}

func enforceStopDeadline() {
	go func() {
		time.Sleep(deferredCleanupTimeout)
		logrus.Error("deferred cleanup timed out, exiting")

		// dump goroutine stacks
		buf := make([]byte, 65536)
		n := runtime.Stack(buf, true)
		err := StopDeadlockError{filterStacks(string(buf[:n]))}
		fmt.Fprintln(os.Stderr, err.Error())

		// try to report to sentry
		_ = util.WithTimeout0(func() {
			sentry.CaptureException(err)
			sentry.Flush(sentryconf.FlushTimeout)
		}, sentryconf.FlushTimeout)

		os.Exit(1)
	}()
}

type VmManager struct {
	stopCh chan<- types.StopRequest
}

func (m *VmManager) Stop(typ types.StopType, reason types.StopReason) {
	m.stopCh <- types.StopRequest{Type: typ, Reason: reason}
}

func runVmManager() {
	// virtiofs in libkrun requires umask=0. set it early to guarantee consistent behavior
	// for all of vmgr
	// careful when binding to unix sockets because of this!
	unix.Umask(0)

	// propagate stop reason via exit code
	lastStopReason := types.StopReasonUnknownCrash
	defer func() {
		if lastStopReason > types.Start_UnexpectedStopReasons {
			os.Exit(stopExitCodeBase + int(lastStopReason-types.Start_UnexpectedStopReasons))
		}
	}()

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
			Dsn:     sentryconf.DSN,
			Release: appver.Get().Short,
		})
		if err != nil {
			logrus.WithError(err).Error("failed to init Sentry")
		}

		sentry.ConfigureScope(func(scope *sentry.Scope) {
			installID := drmid.ReadInstallID()
			logrus.WithField("installID", installID).Debug("setting user")
			scope.SetUser(sentry.User{ID: installID})
		})

		defer sentry.Flush(sentryconf.FlushTimeout)
	}
	// sentry.Recover() suppresses panic
	defer func() {
		if err := recover(); err != nil {
			hub := sentry.CurrentHub()
			hub.Recover(err)

			// don't panic yet here -- we want to make it to lastStopReason's os.Exit call
			// log at fatal level but don't exit yet
			// we've also already lost the stack trace anyway
			logrus.StandardLogger().Fatalf("panic: %v", err)
		}
	}()
	// recover from fatal-log panic:
	// before sentry, so we don't report dummy CLI panic error to sentry
	defer errorx.RecoverCLI()

	if !osver.IsAtLeast("v12.3") {
		errorx.Fatalf("macOS too old - min 12.3")
	}

	// done signal for shutdown process
	// must close this after all cleanup so next start works (incl. closing listeners)
	doneCh := make(chan struct{})
	// close OK: signal select loop
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
		errorx.Fatalf("vmgr is already running (socket)")
	}

	// take the lock
	lockFile, err := flock.Open(conf.VmgrLockFile())
	check(err)
	if waitLock {
		// wait lock for spawn-daemon handoff
		err = util.WithTimeout1(func() error {
			return flock.WaitLock(lockFile)
		}, handoffWaitLockTimeout)
		if err != nil {
			errorx.Fatalf("vmgr is already running (wait lock): %w", err)
		}
	} else {
		err = flock.Lock(lockFile)
		if err != nil {
			errorx.Fatalf("vmgr is already running (lock): %w", err)
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
	_ = os.RemoveAll(conf.RunDir())
	// then recreate because RunDir only ensures once
	err = os.MkdirAll(conf.RunDir(), 0755)
	check(err)

	// write build ID
	if buildID == "" {
		buildID, err = buildid.CalculateCurrent()
		check(err)
	}
	err = os.WriteFile(conf.VmgrVersionFile(), []byte(buildID), 0644)
	check(err)

	// everything is set up for spawn-daemon to work properly (build id and pid)
	// now notify GUI that we've started
	pid := os.Getpid()
	vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
		Vmgr: &uitypes.VmgrEvent{
			NewDaemonPid: &pid,
		},
	})

	// killswitch
	err = killswitch.Check()
	if err != nil {
		errorx.Fatalf("%w", err)
	}
	stopCh := make(chan types.StopRequest, 1)
	killswitch.Watch(func(err error) {
		logrus.WithError(err).Error(err.Error())
		stopCh <- types.StopRequest{Type: types.StopTypeGraceful, Reason: types.StopReasonKillswitch}
	})

	// Rosetta check
	err = verifyRosetta()
	if err != nil {
		errorx.Fatalf("%w", err)
	}

	// state migration
	err = migrateState()
	check(err)

	if _, err := os.Stat(conf.DataImage()); errors.Is(err, os.ErrNotExist) {
		logrus.Info("initializing data")
		extractSparse(streamObfAssetFile("data.img.tar"))
	}

	// create a new empty swap img
	_ = os.Remove(conf.SwapImage())
	err = createEmptySwap()
	check(err)

	// remove legacy logs
	_ = os.Remove(conf.ConsoleLog())

	consoleMode := ConsoleLog
	if useStdioConsole {
		consoleMode = ConsoleStdio
	}

	// wait for data.img flock
	logrus.Debug("waiting for data lock")
	err = ensureDataLock()
	if err != nil {
		errorx.Fatalf("failed to lock data: %w", err)
	}

	// always prefer rsvm
	var monitor vmm.Monitor = rsvm.Monitor
	if conf.Debug() && os.Getenv("VMM") == "vzf" {
		// in debug, allow vzf override for testing
		monitor = vzf.Monitor
	}

	// set time machine backup xattr
	err = util.SetBackupExclude(conf.DataImage(), !vmconfig.Get().DataAllowBackup)
	check(err)
	// always exclude swap
	err = util.SetBackupExclude(conf.SwapImage(), true)
	check(err)
	// update xattr on config change
	go func() {
		for diff := range vmconfig.SubscribeDiff() {
			if diff.New.DataAllowBackup != diff.Old.DataAllowBackup {
				err = util.SetBackupExclude(conf.DataImage(), !diff.New.DataAllowBackup)
				if err != nil {
					logrus.WithError(err).Error("failed to set backup exclude on data image")
				}
			}
		}
	}()

	// force-unmount nfs
	// needed to make it safe to stat this later for virtio nfs loop protection
	err = nfsmnt.UnmountNfs()
	// EINVAL = not mounted, ENOENT = mountpoint not created
	if err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
		logrus.WithError(err).Error("failed to unmount nfs")
	}

	logrus.Debug("configuring VM")
	healthCheckCh := make(chan struct{}, 1)
	shutdownWg := &sync.WaitGroup{}
	vnetwork, vm := CreateVm(monitor, &VmParams{
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
		// doesn't work on vzf so let's just hide it
		Balloon: false,
		Rng:     true,
		// no longer used (NFS is now TCP)
		Vsock:    false,
		Virtiofs: true,
		Rosetta:  vmconfig.Get().Rosetta,
		// useful once we have graphics
		Sound: false,

		StopCh:        stopCh,
		HealthCheckCh: healthCheckCh,
	}, shutdownWg)
	defer vnetwork.Close()
	if monitor != rsvm.Monitor {
		defer runOne("flush disk", flushDisk)
	}
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
	go runOne("host bridge setting monitor", func() error {
		vnetwork.MonitorHostBridgeSetting()
		return nil
	})
	if vmconfig.Get().NetworkBridge {
		runAsyncInitTask("scon host bridge", vnetwork.CreateSconMachineHostBridge)
	}

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
				stopCh <- types.StopRequest{Type: types.StopTypeGraceful, Reason: types.StopReasonDrm}
				return
			case <-doneCh:
				return
			}
		}
	}()

	// Services
	hcServer := services.StartNetServices(vnetwork, drmClient)
	// TODO: for LAN mDNS - refresh default interface
	//vnetwork.SetOnRefreshMdns(hcServer.HostMdns.UpdateInterfaces)

	// VM control server client
	vc, err := vclient.NewWithNetwork(vnetwork, vm, stopCh, healthCheckCh)
	hcServer.Vclient = vc
	check(err)
	defer vc.Close()
	err = vc.StartBackground()
	check(err)

	// fsnotifier
	fsNotifier, err := fsnotify.NewVmNotifier(vnetwork, monitor == rsvm.Monitor)
	check(err)
	rsvm.OnFsActivityCallback = fsNotifier.OnVirtiofsActivity
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
				stopCh <- types.StopRequest{Type: types.StopTypeForce, Reason: types.StopReasonSignal}
			} else {
				logrus.Info("Received signal, requesting stop")
				stopCh <- types.StopRequest{Type: types.StopTypeGraceful, Reason: types.StopReasonSignal}
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
		drm:          drmClient,
		network:      vnetwork,
		hcontrol:     hcServer,
	}
	controlServer.setupUserDetailsOnce = sync.OnceValues(controlServer.doGetUserDetailsAndSetupEnv)
	controlServer.uiEventDebounce = *syncx.NewLeadingFuncDebounce(uitypes.UIEventDebounce, func() {
		vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
			Vmgr: &uitypes.VmgrEvent{
				VmConfig: vmconfig.Get(),
				DrmState: drmClient.GenerateUIState(),
			},
		})
	})
	vmcontrolCleanup, err := controlServer.Serve()
	check(err)
	defer vmcontrolCleanup()

	// Host forwards (setup vsock)
	essentialForwards := map[string]string{
		// unix sockets
		"unix:" + conf.DockerSocket():  "tcp:" + str(ports.GuestDocker),
		"unix:" + conf.SconSSHSocket(): "tcp:" + str(ports.GuestSconSSH),
		"unix:" + conf.SconRPCSocket(): "tcp:" + str(ports.GuestScon),
		// NFS is special, handled below
	}
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
			errorx.Fatalf("host forward failed: spec=%v err=%w", spec, err)
		}
	}
	optionalForwards := optionalForwardsLocalhost
	if vmconfig.Get().SSHExposePort {
		optionalForwards = optionalForwardsPublic
	}
	for fromSpec, toSpec := range optionalForwards {
		spec := vnet.ForwardSpec{Host: fromSpec, Guest: toSpec}
		_, err := vnetwork.StartForward(spec)
		if err != nil {
			logrus.WithError(err).WithField("spec", spec).Error("host forward failed")
		}
	}

	// special NFS forward
	// tcp is faster and probably more stable
	nfsFwdSpec := vnet.ForwardSpec{
		// dynamically assigned port
		Host:  "tcp:127.0.0.1:0",
		Guest: "tcp:" + str(ports.GuestNFS),
	}
	nfsFwd, err := vnetwork.StartForward(nfsFwdSpec)
	if err != nil {
		errorx.Fatalf("host forward failed: %w", err)
	}
	nfsPort := nfsFwd.(*tcpfwd.TcpHostForward).TcpPort()
	hcServer.NfsPort = nfsPort

	defer os.Remove(conf.DockerSocket())
	defer os.Remove(conf.SconRPCSocket())
	defer os.Remove(conf.SconSSHSocket())

	// Docker context and certs.d
	runAsyncInitTask("Docker context", func() error {
		// PATH for hostssh, DOCKER_CONFIG for docker cli
		// blocking here because docker depends on it
		_, err := controlServer.setupUserDetailsOnce()
		if err != nil {
			logrus.WithError(err).Error("failed to set up environment")
		}

		err = setupDockerContext()
		if err != nil {
			logrus.WithError(err).Error("failed to set Docker context")
		}

		return nil
	})

	// status dir
	runAsyncInitTask("status file", func() error {
		return os.WriteFile(conf.StatusFileRunning(), []byte{}, 0644)
	})
	defer os.Remove(conf.StatusFileRunning())

	// Mount NFS
	defer hcServer.InternalUnmountNfs()

	// the last defer: deadlock breaker
	defer enforceStopDeadline()

	// notify GUI that host-side startup is done
	vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
		Vmgr: &uitypes.VmgrEvent{
			StateReady: true,
			// and give it an initial config
			VmConfig: vmconfig.Get(),
			// DRM state is probably not ready yet, don't try to seed it early
		},
	})

	err = controlServer.onStart()
	if err != nil {
		logrus.WithError(err).Error("vmcontrol start hook failed")
	}

	logrus.Debug("waiting for VM to start")
	returnCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	for {
		select {
		case <-returnCh:
			return

		case stopReq := <-stopCh:
			logrus.WithField("reason", stopReq.Reason).Info("stop requested")
			lastStopReason = stopReq.Reason
			// attempt to unmount nfs first
			_ = hcServer.InternalUnmountNfs()

			go func() {
				switch stopReq.Type {
				case types.StopTypeForce:
					err := vm.ForceStop()
					if err != nil {
						logrus.WithError(err).Error("VM force stop failed")
						returnCh <- struct{}{}
					}
				case types.StopTypeGraceful:
					err := tryGracefulStop(vm, vc)
					if err != nil {
						logrus.WithError(err).Error("VM graceful stop failed")
						returnCh <- struct{}{}
					}
				}
			}()

		case newState := <-stateChan:
			switch newState {
			case vmm.MachineStateStarting:
				logrus.Info("[VM] starting")
			case vmm.MachineStateStopping:
				logrus.Info("[VM] stopping")
			case vmm.MachineStateRunning:
				logrus.Info("[VM] started")
			case vmm.MachineStateStopped:
				logrus.Info("[VM] stopped")
				err = controlServer.onStop()
				if err != nil {
					logrus.WithError(err).Error("vmcontrol stop hook failed")
					return
				}

				// close files (console pipe)
				err = vm.Close()
				if err != nil {
					logrus.WithError(err).Error("VM close failed")
					return
				}

				// wait for shutdown tasks
				// (flush console pipe)
				shutdownWg.Wait()

				// non-blocking select to see if there's a pending stop request that gives us a reason
				select {
				case stopReq := <-stopCh:
					logrus.WithField("reason", stopReq.Reason).Debug("backfilling stop reason")
					lastStopReason = stopReq.Reason
				default:
				}

				return
			case vmm.MachineStateError:
				logrus.Error("[VM] error")
				return
			case vmm.MachineStatePaused:
				logrus.Debug("[VM] paused")
			case vmm.MachineStateResuming:
				logrus.Debug("[VM] resuming")
			case vmm.MachineStatePausing:
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
	case "uninstall-privhelper":
		runUninstallPrivhelper()
	case "_set-refresh-token":
		runSetRefreshToken()
	case "_check-refresh-token":
		runCheckRefreshToken()
	case "vmgr", "":
		runVmManager()
	default:
		panic("unknown command: " + cmd)
	}
}
