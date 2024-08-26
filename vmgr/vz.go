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
	"golang.org/x/sys/unix"
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
	KernelCsmap        string
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

func buildCmdline(monitor vmm.Monitor, params *VmParams) string {
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
		// don't reserve 64M (wasting struct pages) for bounce buffers
		// all 64 bits are DMA-able
		"swiotlb=noforce",
		// give slab allocator 16K (4 pages) at a time, to reduce 4K-16K fragmentation for arm64 balloon
		// should also be good for perf
		// TODO: disable on 16k kernels
		"slab_min_order=2",
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

	if runtime.GOARCH == "arm64" {
		// on M3+, use CNTVCTSS_EL0 to omit ISB before CNTVCT_EL0 reads
		// this brings counter read down from 8ns -> 4ns
		// sysctl reports FEAT_ECV as supported on M3+, but HVF masks it out because it's primarily a virtualization feature (CNTPOFF_EL2) and it doesn't support nested virt
		if feat, err := unix.SysctlUint32("hw.optional.arm.FEAT_ECV"); err == nil && feat == 1 {
			// depends on kernel commit to allow ID_AA64MMFR0_EL1.ECV=1
			cmdline = append(cmdline, "id_aa64mmfr0.e=1")
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

		// if possible, use vport instead of HVC for userspace writes, so that guest waits on IRQ if pipe is full instead of spinning
		// kernel can only use HVC
		if monitor == rsvm.Monitor {
			// TODO: index could change from virtio2
			// port index = 2 (0 = console, 1 = stdin, 2 = stdout)
			cmdline = append(cmdline, "orb.console=/dev/vport2p2")
		}
	}

	return strings.Join(cmdline, " ")
}

func CreateVm(monitor vmm.Monitor, params *VmParams, shutdownWg *sync.WaitGroup) (retNet *vnet.Network, retMachine vmm.Machine, retErr error) {
	cmdline := buildCmdline(monitor, params)
	logrus.Debug("cmdline", cmdline)

	spec := vmm.VzSpec{
		Cpus:             params.Cpus,
		Memory:           params.Memory * 1024 * 1024,
		Kernel:           params.Kernel,
		KernelCsmap:      params.KernelCsmap,
		Cmdline:          cmdline,
		MacAddressPrefix: params.MacAddressPrefix,
		NetworkNat:       params.NetworkNat,
		// must be empty vec, not null, for rust
		NetworkFds:   []int{},
		NetworkSwift: []uintptr{},
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
		if err != nil {
			return nil, nil, err
		}
		dirStatSys := dirStat.Sys().(*syscall.Stat_t)

		// same for parent. needed to handle lookup case (with no preceding readdir)
		parentDirPath := filepath.Dir(coredir.NfsMountpoint())
		parentDirStat, err := os.Stat(parentDirPath)
		if err != nil {
			return nil, nil, err
		}
		parentDirStatSys := parentDirStat.Sys().(*syscall.Stat_t)

		// also stat /var/empty
		emptyDirStat, err := os.Stat("/var/empty")
		if err != nil {
			return nil, nil, err
		}
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
	var conProcessor *ConsoleProcessor
	retainFiles := []*os.File{}
	if params.Console != ConsoleNone {
		var conRead, conWrite *os.File
		switch params.Console {
		case ConsoleStdio:
			conRead = os.Stdin
			conWrite = os.Stdout
		case ConsoleLog:
			// libkrun can't register /dev/null with kqueue for read readiness notifications, so make a pipe
			var conReadWrite *os.File
			conRead, conReadWrite, err = os.Pipe()
			if err != nil {
				return nil, nil, err
			}

			// TODO: save this and write sysrqs to it
			conReadWrite.Close()

			conProcessor, conWrite, err = NewConsoleProcessor(params.StopCh, params.HealthCheckCh, shutdownWg)
			if err != nil {
				return nil, nil, fmt.Errorf("new console processor: %w", err)
			}
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
		opts := vnet.NetOptions{
			LinkMTU:      uint32(mtu),
			WantsVnetHdr: monitor.NetworkWantsVnetHdrV1(),
		}

		if monitor == rsvm.Monitor {
			// vnet = index 0
			cb := rsvm.NewNetCallbacks(rsvm.HandleGvisor)
			newNetwork, netHandle, err := vnet.StartCallbackPair(opts, cb)
			if err != nil {
				return nil, nil, fmt.Errorf("start vnet: %w", err)
			}
			defer func() {
				if retErr != nil {
					netHandle.Delete()
				}
			}()
			vnetwork = newNetwork

			spec.NetworkGvisor = uintptr(netHandle)
		} else {
			newNetwork, gvnetFile, err := vnet.StartUnixgramPair(opts)
			if err != nil {
				return nil, nil, fmt.Errorf("start vnet: %w", err)
			}
			vnetwork = newNetwork

			spec.NetworkFds = append(spec.NetworkFds, int(util.GetFd(gvnetFile)))
			// already retained by network, but doesn't hurt
			retainFiles = append(retainFiles, gvnetFile)
		}
	}
	for i := 0; i < params.NetworkHostBridges; i++ {
		// Swift handles are bridge indexes
		if monitor == rsvm.Monitor {
			spec.NetworkSwift = append(spec.NetworkSwift, uintptr(i))

			// ... and this is now a Rust handle
			rsvmHandle := rsvm.HandleHostBridgesStart + uintptr(i)
			err = vnetwork.AddHostBridge(vzf.NetHandleFromRsvmHandle(rsvmHandle))
			if err != nil {
				return nil, nil, fmt.Errorf("add host bridge fd: %w", err)
			}
		} else {
			// host bridges are reserved but backends won't be created until later
			file0, fd1, err := vnet.NewUnixgramPair()
			if err != nil {
				return nil, nil, fmt.Errorf("new unixgram pair: %w", err)
			}

			// use util.GetFd to preserve nonblock
			spec.NetworkFds = append(spec.NetworkFds, int(util.GetFd(file0)))
			retainFiles = append(retainFiles, file0)

			// keep fd1 for bridge management
			err = vnetwork.AddHostBridge(vzf.NetHandleFromFd(fd1))
			if err != nil {
				return nil, nil, fmt.Errorf("add host bridge fd: %w", err)
			}
		}
	}
	if params.NetworkPairFile != nil {
		spec.NetworkFds = append(spec.NetworkFds, int(util.GetFd(params.NetworkPairFile)))
		retainFiles = append(retainFiles, params.NetworkPairFile)
	}

	// install Rosetta
	if params.Rosetta {
		rosettaStatus, err := vzf.SwextInstallRosetta()
		if err != nil {
			return nil, nil, fmt.Errorf("install rosetta: %w", err)
		}

		if rosettaStatus != vzf.RosettaStatusInstalled {
			logrus.Info("Rosetta not supported or install canceled; saving preference")
			err := vmconfig.Update(func(c *vmconfig.VmConfig) {
				c.Rosetta = false
			})
			if err != nil {
				return nil, nil, fmt.Errorf("save rosetta preference: %w", err)
			}

			params.Rosetta = false
			spec.Rosetta = false
		}
	}

	// must retain files or they get closed by Go finalizer!
	// causes flaky console
	vm, err := monitor.NewMachine(&spec, retainFiles)
	if err != nil {
		return nil, nil, fmt.Errorf("new machine: %w", err)
	}

	// set machine on conProcessor
	if conProcessor != nil {
		conProcessor.SetMachine(vm)
	}

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
					_ = rsvm.SetRinitData([]byte{})
					err = vm.ForceStop()
					if err != nil {
						logrus.WithError(err).Error("failed to force stop VM after rinit")
					}

					return
				}

				// report to rsvm
				logrus.Debug("finishing rinit")
				err = rsvm.SetRinitData(rinitData.Data)
				if err != nil {
					logrus.WithError(err).Error("failed to set rdata")
				}
			}()
		}
	}

	return vnetwork, vm, nil
}
