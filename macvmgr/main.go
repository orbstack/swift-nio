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
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/kdrag0n/macvirt/macvmgr/buildid"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appver"
	"github.com/kdrag0n/macvirt/macvmgr/conf/nfsmnt"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/drm"
	"github.com/kdrag0n/macvirt/macvmgr/drm/killswitch"
	"github.com/kdrag0n/macvirt/macvmgr/flock"
	"github.com/kdrag0n/macvirt/macvmgr/fsnotify"
	"github.com/kdrag0n/macvirt/macvmgr/osver"
	"github.com/kdrag0n/macvirt/macvmgr/vclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/macvmgr/vmconfig"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/tcpfwd"
	"github.com/kdrag0n/macvirt/macvmgr/vzf"
	"github.com/kdrag0n/macvirt/scon/isclient"
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

	gracefulStopTimeout   = 15 * time.Second
	sentryShutdownTimeout = 2 * time.Second
)

const (
	nfsReadmeText = `# OrbStack file sharing

When OrbStack is running, this folder contains Docker volumes and Linux machines. All Docker and Linux files can be found here.

This folder is empty when OrbStack is not running. Do not put files here.

For more details, see:
    - https://docs.orbstack.dev/readme-link/docker-mount
    - https://docs.orbstack.dev/readme-link/machine-mount
`
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
	var errBuf bytes.Buffer
	createCmd := exec.Command(dockerBin, "context", "create", appid.AppName, "--description", appid.UserAppName, "--docker", "host=unix://"+conf.DockerSocket())
	createCmd.Stderr = &errBuf
	err := createCmd.Run()
	if err != nil {
		if strings.Contains(errBuf.String(), "already exists") {
			// ignore and continue to set use
		} else {
			return err
		}
	}

	// use context
	err = exec.Command(dockerBin, "context", "use", appid.AppName).Run()
	if err != nil {
		return err
	}

	return nil
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
		if isMountpoint(linuxDir) {
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
		err = os.Symlink(conf.NfsMountpoint(), linuxDir)
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
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
	})

	if !conf.Debug() {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:     "https://4fa1e6255f764440a7183d2947f4bc8e@o120089.ingest.sentry.io/4504665519554560",
			Release: appver.Get().Short,
		})
		if err != nil {
			logrus.WithError(err).Error("failed to init Sentry")
		}
		defer sentry.Flush(sentryShutdownTimeout)
		defer sentry.Recover()
	}

	if !osver.IsAtLeast("v12.3") {
		logrus.Fatal("macOS too old - min 12.3")
	}

	// done signal for shutdown process
	// must close this after all cleanup so next start works (incl. closing listeners)
	doneCh := make(chan struct{})
	defer close(doneCh)

	// parse args
	var buildID string
	var isRetry bool
	flag.StringVar(&buildID, "build-id", "", "build ID")
	flag.BoolVar(&isRetry, "retry", false, "retry")
	if len(os.Args) > 2 {
		err := flag.CommandLine.Parse(os.Args[2:])
		check(err)
	}
	if isRetry {
		logrus.Info("retrying vmgr launch")
	}

	// ensure it's not running
	if vmclient.IsRunning() {
		logrus.Fatal("vmgr is already running (socket)")
	}

	// take the lock
	lockFile, err := flock.Open(conf.VmgrLockFile())
	check(err)
	err = flock.Lock(lockFile)
	if err != nil {
		logrus.Fatal("vmgr is already running (lock): ", err)
	}
	defer func() {
		err := flock.Unlock(lockFile)
		if err != nil {
			logrus.WithError(err).Error("failed to unlock")
		}
	}()

	// remove everything in run, sockets and pid
	os.RemoveAll(conf.RunDir())

	// write build ID
	if buildID == "" {
		buildID, err = buildid.CalculateCurrent()
		check(err)
	}
	err = os.WriteFile(conf.VmgrTimestampFile(), []byte(buildID), 0644)
	check(err)

	// killswitch
	err = killswitch.Check()
	check(err)
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
			logrus.Fatal("APFS is required")
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
	params := &VmParams{
		Cpus: vmconfig.Get().CPU,
		// default memory algo = 1/3 of host memory, max 10 GB
		Memory: vmconfig.Get().MemoryMiB,
		Kernel: conf.GetAssetFile("kernel"),
		// this one uses gvproxy ssh
		Console:          consoleMode,
		DiskRootfs:       conf.GetAssetFile("rootfs.img"),
		DiskData:         conf.DataImage(),
		DiskSwap:         conf.SwapImage(),
		NetworkVnet:      true,
		NetworkNat:       useNat,
		MacAddressPrefix: "86:6c:f1:2e:9e",
		Balloon:          true,
		Rng:              true,
		Vsock:            true,
		Virtiofs:         true,
		Rosetta:          vmconfig.Get().Rosetta,
		Sound:            false,
	}

	logrus.Info("configuring VM")
	vnetwork, vm := CreateVm(params)
	defer vnetwork.Close()
	// close in case we need to release disk flock for next start
	defer vm.Close()

	// load proxy settings and proxy password (keychain prompt)
	go func() {
		err := vnetwork.Proxy.Refresh()
		if err != nil {
			logrus.WithError(err).Error("failed to load proxy settings")
		}
	}()

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
	vc, err := vclient.NewWithNetwork(vnetwork, vm)
	check(err)
	defer vc.Close()
	err = vc.StartBackground()
	check(err)

	// fsnotifier
	fsNotifier, err := fsnotify.NewVmNotifier(drm.Client().SconInternalClientsCh())
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
	}
	unixListener, err := controlServer.Serve()
	check(err)
	defer unixListener.Close()

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

	defer os.Remove(conf.DockerSocket())
	defer os.Remove(conf.SconRPCSocket())
	defer os.Remove(conf.SconSSHSocket())

	// Docker context and certs.d
	go func() {
		// PATH for hostssh
		// blocking here because docker depends on it
		runOne("PATH setup", setupPath)

		err := setupDockerContext()
		if err != nil {
			logrus.WithError(err).Error("failed to set Docker context")
		}
	}()

	// SSH key and config
	go runOne("public SSH setup", setupPublicSSH)

	// Mount NFS
	nfsMounted := false
	go func() {
		// prep: create nfs dir, write readme, make read-only
		dir := conf.NfsMountpoint()
		// only if not mounted yet
		if !isMountpoint(dir) {
			// conf.NfsMountpoint() already calls mkdir
			err := os.WriteFile(dir+"/README.txt", []byte(nfsReadmeText), 0644)
			// permission error is normal, that means it's already read only
			if err != nil && !errors.Is(err, os.ErrPermission) {
				logrus.WithError(err).Error("failed to write NFS readme")
			}
			err = os.Chmod(dir, 0555)
			if err != nil {
				logrus.WithError(err).Error("failed to chmod NFS dir")
			}
		}

		vc.WaitForDataReady()

		defer func() {
			if nfsMounted {
				logrus.Debug("Reporting NFS to scon")

				// report to scon so it can mount nfs root
				err = drm.Client().UseSconInternalClient(func(scon *isclient.Client) error {
					return scon.OnNfsMounted()
				})
				if err != nil {
					logrus.WithError(err).Error("failed to report NFS mounted to scon")
				}

				logrus.Debug("Reporting NFS to scon done")
			}
		}()

		// vsock fails immediately unlike tcp dialing, so try 5 times
		for i := 0; i < nfsMountTries; i++ {
			logrus.Info("Mounting NFS...")
			err := nfsmnt.MountNfs(nfsPort)
			if err != nil {
				// if already mounted, we'll just reuse it
				// careful, this could hang
				if isMountpoint(dir) {
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
			err := nfsmnt.UnmountNfs()
			if err != nil {
				logrus.WithError(err).Error("NFS unmount failed")
			}
			logrus.Info("NFS unmounted")
			nfsMounted = false
		}
	}
	defer unmountNfs()

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
			unmountNfs()

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
	} else {
		fmt.Fprintln(os.Stderr, "usage: "+os.Args[0]+" <command>")
		os.Exit(1)
	}

	switch cmd {
	case "spawn-daemon":
		runSpawnDaemon()
	case "ssh-proxy-fdpass":
		runSshProxyFdpass()
	case "report-env":
		runReportEnv()
	case "vmgr":
		runVmManager()
	default:
		panic("unknown command: " + cmd)
	}
}
