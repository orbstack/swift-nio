package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/osver"
	"github.com/orbstack/macvirt/vmgr/rsvm"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
	"golang.org/x/term"
)

type ConsoleMode int

const (
	ConsoleNone ConsoleMode = iota
	ConsoleStdio
	ConsoleLog
)

type VmParams struct {
	Cpus               int
	Memory             uint64
	Kernel             string
	Console            ConsoleMode
	DiskRootfs         string
	DiskData           string
	DiskSwap           string
	NetworkNat         bool
	NetworkVnet        bool
	NetworkHostBridges int
	NetworkPairFile    *os.File
	MacAddressPrefix   string
	Balloon            bool
	Rng                bool
	Vsock              bool
	Virtiofs           bool
	Rosetta            bool
	Sound              bool

	StopCh        chan<- types.StopRequest
	HealthCheckCh chan<- struct{}
}

type RinitData struct {
	Data []byte
}

func RunRinitVm() (*RinitData, error) {
	// read fd = /dev/null
	conRead, err := os.Open("/dev/null")
	if err != nil {
		return nil, fmt.Errorf("open read: %w", err)
	}
	defer conRead.Close()

	// write fd = stdout
	pipeRead, conWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}
	defer pipeRead.Close()
	defer conWrite.Close()

	// bare minimum for VM to boot
	spec := vmm.VzSpec{
		Cpus:   1,
		Memory: 32 * 1024 * 1024, // 32M
		Kernel: conf.GetAssetFile("kernel.ri"),
		// no quiet: kernel is built without printk
		// arm64 only
		Cmdline:    "console=hvc0 swiotlb=noforce",
		Initrd:     conf.GetAssetFile("rpack"),
		NetworkFds: []int{},
		Rosetta:    true,
		Console: &vmm.ConsoleSpec{
			ReadFd:  int(conRead.Fd()),
			WriteFd: int(conWrite.Fd()),
		},
	}

	machine, err := vzf.Monitor.NewMachine(&spec, []*os.File{conRead, conWrite})
	if err != nil {
		return nil, fmt.Errorf("new machine: %w", err)
	}
	defer machine.Close()
	defer machine.ForceStop()

	// read from pipe
	pipeDataChan := make(chan []byte, 1)
	go func() {
		data, err := io.ReadAll(pipeRead)
		if err != nil {
			logrus.WithError(err).Error("read from pipe failed")
			pipeDataChan <- nil
		} else {
			pipeDataChan <- data
		}
	}()

	err = machine.Start()
	if err != nil {
		return nil, fmt.Errorf("start machine: %w", err)
	}

	// wait for stop or error
	stateChan := machine.StateChan()
	for state := range stateChan {
		logrus.WithField("state", state).Debug("rinit vm state")
		if state == vmm.MachineStateError {
			return nil, fmt.Errorf("rinit vm error")
		} else if state == vmm.MachineStateStopped {
			break
		}
	}

	// close pipe
	conWrite.Close()
	// wait for result
	data := <-pipeDataChan
	if data == nil {
		return nil, fmt.Errorf("read from pipe failed")
	}

	return &RinitData{Data: data}, nil
}

func CreateVm(monitor vmm.Monitor, params *VmParams, shutdownWg *sync.WaitGroup) (*vnet.Network, vmm.Machine) {
	cmdline := []string{
		// boot
		"init=/opt/orb/vinit",
		// userspace
		"orb.data_size=" + strconv.FormatUint(conf.DiskSize(), 10),
		"orb.host_major_version=" + osver.Major(),
		"orb.host_build_version=" + osver.Build(),
		// Kernel tuning
		"workqueue.power_efficient=1",
		"cgroup.memory=nokmem,nosocket",
		// rcu_nocbs is in kernel
		// Drivers
		"nbd.nbds_max=4",    // fast boot
		"can.stats_timer=0", // periodic timer
	}
	if runtime.GOARCH == "amd64" {
		// on ARM: kpti is free with E0PD
		// But on x86, there are too many, just disable it like Docker
		// Also prevent TSC from being disabled after sleep with tsc=reliable
		cmdline = append(cmdline, "mitigations=off", "clocksource=tsc", "tsc=reliable")
		if monitor == vzf.Monitor {
			// on vzf: disable HPET to fix high idle CPU usage & wakeups, especially with high CONFIG_HZ=1000
			cmdline = append(cmdline, "hpet=disable")
		}
	}
	if params.DiskRootfs != "" {
		cmdline = append(cmdline, "root=/dev/vda", "rootfstype=erofs", "ro")
	}
	if params.Console != ConsoleNone {
		// quiet kernel boot to reduce log spam when truncated in sentry and GUI
		// disabled once init starts to preserve any debug info
		cmdline = append(cmdline, "console=hvc0", "quiet")
		// disable colors if logging to file
		if params.Console == ConsoleLog && !term.IsTerminal(int(os.Stdout.Fd())) {
			cmdline = append(cmdline, "orb.console_is_pipe")
		}
	}
	// dogfood: disable swiotlb to save 64M reserved memory
	// TODO enable this for everyon, stress test all devices, test on x86
	if conf.Debug() {
		cmdline = append(cmdline, "swiotlb=noforce", "iommu=off")
	}
	logrus.Debug("cmdline", cmdline)

	spec := vmm.VzSpec{
		Cpus:             params.Cpus,
		Memory:           params.Memory * 1024 * 1024,
		Kernel:           params.Kernel,
		Cmdline:          strings.Join(cmdline, " "),
		MacAddressPrefix: params.MacAddressPrefix,
		NetworkNat:       params.NetworkNat,
		/* fds populated below */
		Rng:        params.Rng,
		DiskRootfs: params.DiskRootfs,
		DiskData:   params.DiskData,
		DiskSwap:   params.DiskSwap,
		Balloon:    params.Balloon,
		Vsock:      params.Vsock,
		Virtiofs:   params.Virtiofs,
		Rosetta:    params.Rosetta,
		Sound:      params.Sound,
	}

	// FS: virtiofs<->nfs deadlock/loop prevention
	if params.Virtiofs {
		// create if not exists
		dirPath := coredir.EnsureNfsMountpoint()
		dirName := filepath.Base(coredir.NfsMountpoint())

		// follow symlinks (no lstat). safe because it's guaranteed to be unmounted,
		// and FUSE server never follows symlinks -- every lookup returns symlinks
		dirStat, err := os.Stat(dirPath)
		check(err)
		dirStatSys := dirStat.Sys().(*syscall.Stat_t)

		// same for parent. needed to handle lookup case (with no preceding readdir)
		parentDirPath := filepath.Dir(coredir.NfsMountpoint())
		parentDirStat, err := os.Stat(parentDirPath)
		check(err)
		parentDirStatSys := parentDirStat.Sys().(*syscall.Stat_t)

		// also stat /var/empty
		emptyDirStat, err := os.Stat("/var/empty")
		check(err)
		emptyDirStatSys := emptyDirStat.Sys().(*syscall.Stat_t)

		spec.NfsInfo = &vmm.NfsInfo{
			DirDev:         dirStatSys.Dev,
			DirInode:       dirStatSys.Ino,
			DirName:        dirName,
			ParentDirDev:   parentDirStatSys.Dev,
			ParentDirInode: parentDirStatSys.Ino,
			EmptyDirInode:  emptyDirStatSys.Ino,
		}
	}

	// Console
	var err error
	retainFiles := []*os.File{}
	if params.Console != ConsoleNone {
		var conRead, conWrite *os.File
		switch params.Console {
		case ConsoleStdio:
			conRead = os.Stdin
			conWrite = os.Stdout
		case ConsoleLog:
			conRead, err = os.Open("/dev/null")
			check(err)
			conWrite, err = NewConsoleLogPipe(params.StopCh, params.HealthCheckCh, shutdownWg)
			check(err)
		}

		spec.Console = &vmm.ConsoleSpec{
			ReadFd:  int(conRead.Fd()),
			WriteFd: int(conWrite.Fd()),
		}
		retainFiles = append(retainFiles, conRead, conWrite)
	}

	// Network
	mtu := monitor.NetworkMTU()
	spec.Mtu = mtu
	// gvnet
	var vnetwork *vnet.Network
	if params.NetworkVnet {
		newNetwork, gvnetFile, err := vnet.StartUnixgramPair(vnet.NetOptions{
			LinkMTU:      uint32(mtu),
			WantsVnetHdr: monitor.NetworkWantsVnetHdrV1(),
		})
		check(err)
		vnetwork = newNetwork

		spec.NetworkFds = append(spec.NetworkFds, int(util.GetFd(gvnetFile)))
		// already retained by network, but doesn't hurt
		retainFiles = append(retainFiles, gvnetFile)
	}
	for i := 0; i < params.NetworkHostBridges; i++ {
		// host bridges are only reserved, not
		file0, fd1, err := vnet.NewUnixgramPair()
		check(err)

		// use util.GetFd to preserve nonblock
		spec.NetworkFds = append(spec.NetworkFds, int(util.GetFd(file0)))
		retainFiles = append(retainFiles, file0)

		// keep fd1 for bridge management
		err = vnetwork.AddHostBridgeFd(fd1)
		check(err)
	}
	if params.NetworkPairFile != nil {
		spec.NetworkFds = append(spec.NetworkFds, int(util.GetFd(params.NetworkPairFile)))
		retainFiles = append(retainFiles, params.NetworkPairFile)
	}

	// install Rosetta
	if params.Rosetta {
		rosettaStatus, err := vzf.SwextInstallRosetta()
		check(err)
		if rosettaStatus != vzf.RosettaStatusInstalled {
			logrus.Info("Rosetta not supported or install canceled; saving preference")
			err := vmconfig.Update(func(c *vmconfig.VmConfig) {
				c.Rosetta = false
			})
			check(err)

			params.Rosetta = false
			spec.Rosetta = false
		}
	}

	// must retain files or they get closed by Go finalizer!
	// causes flaky console
	vm, err := monitor.NewMachine(&spec, retainFiles)
	check(err)

	if params.Rosetta {
		// if it's not VZF, we need to get rinit data from VZF
		// takes ~90ms so do it async. not worth caching
		if monitor != vzf.Monitor {
			go func() {
				logrus.Debug("running rinit")
				rinitData, err := RunRinitVm()
				if err != nil {
					logrus.WithError(err).Error("failed to run rinit")

					// set empty data (to prevent hang), then force shutdown
					rsvm.SetRinitData([]byte{})
					err = vm.ForceStop()
					if err != nil {
						logrus.WithError(err).Error("failed to force stop VM after rinit")
					}

					return
				}

				// report to rsvm
				logrus.Debug("finishing rinit")
				rsvm.SetRinitData(rinitData.Data)
			}()
		}
	}

	return vnetwork, vm
}
