package vclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/util/debugutil"
	"github.com/orbstack/macvirt/vmgr/vclient/iokit"
	"github.com/orbstack/macvirt/vmgr/vclient/vinitclient"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

const (
	// match chrony ntp polling interval
	diskStatsInterval               = 128 * time.Second
	healthCheckSleepWakeGracePeriod = vinitclient.RequestTimeout

	// TODO: fix health check
	// sometimes it fails during sleep on arm64
	// level=error msg="health check failed" error="Post \"http://vcontrol/disk/report_stats\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)"
	stopOnHealthCheckFail = false

	pauseDebounceDelay = 60 * time.Second
)

type VClient struct {
	*vinitclient.VinitClient
	lastStats HostDiskStats
	dataFile  *os.File
	vm        vmm.Machine

	signalStopCh     chan struct{}
	requestStopCh    chan<- types.StopRequest
	healthCheckReqCh <-chan struct{}
}

type HostDiskStats struct {
	HostFsFree  uint64 `json:"hostFsFree"`
	DataImgSize uint64 `json:"dataImgSize"`
}

func newWithTransport(dialFunc vinitclient.DialContextFunc, vm vmm.Machine, requestStopCh chan<- types.StopRequest, healthCheckReqCh <-chan struct{}) (*VClient, error) {
	httpClient := vinitclient.NewVinitClient(dialFunc)
	dataFile, err := os.OpenFile(conf.DataImage(), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &VClient{
		VinitClient:      httpClient,
		dataFile:         dataFile,
		vm:               vm,
		signalStopCh:     make(chan struct{}),
		requestStopCh:    requestStopCh,
		healthCheckReqCh: healthCheckReqCh,
	}, nil
}

func NewWithNetwork(n *vnet.Network, vm vmm.Machine, requestStopCh chan<- types.StopRequest, healthCheckReqCh <-chan struct{}) (*VClient, error) {
	dialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return gonet.DialContextTCP(ctx, n.Stack, tcpip.FullAddress{
			Addr: n.GuestAddr4,
			Port: ports.GuestVcontrol,
		}, ipv4.ProtocolNumber)
	}
	return newWithTransport(dialFunc, vm, requestStopCh, healthCheckReqCh)
}

func (vc *VClient) StartBackground() error {
	mon, err := iokit.MonitorSleepWake()
	if err != nil {
		return fmt.Errorf("register iokit: %w", err)
	}

	// Report disk stats periodically, respond to health check requests
	go func() {
		ticker := time.NewTicker(diskStatsInterval)
		defer ticker.Stop()

		for {
			select {
			case <-vc.signalStopCh:
				return

			case <-ticker.C:
				vc.healthCheck()

			case <-vc.healthCheckReqCh:
				vc.healthCheck()
			}
		}
	}()

	// notify VM of sleep and wake
	// separate goroutine to avoid blocking health check if these requests hang
	vmconfigDiffs := vmconfig.SubscribeDiff()
	pauseDebounce := syncx.NewFuncDebounce(pauseDebounceDelay, func() {
		if iokit.IsAsleep() {
			err := vc.Post("sys/sleep", nil, nil)
			if err != nil {
				logrus.WithError(err).Error("failed to notify VM of sleep")
			}
		}
	})
	go func() {
		for {
			select {
			case <-vc.signalStopCh:
				return

			case diff := <-vmconfigDiffs:
				// if pause-on-sleep is being disabled, then unpause the VM if it was paused
				if diff.New.Power_PauseOnSleep != diff.Old.Power_PauseOnSleep && !diff.New.Power_PauseOnSleep {
					// if awake, then it must already be unpaused, or will be unpaused soon
					if iokit.IsAsleep() {
						err := vc.Post("sys/wake", nil, nil)
						if err != nil {
							logrus.WithError(err).Error("failed to notify VM of wakeup")
						}
					}
				}

			case <-mon.StateChangeChan:
				// this is a saturated "change event" signal. check the current state
				if iokit.IsAsleep() {
					logrus.Info("sleep")

					// notify VM of sleep
					// currently, this is only responsible for pausing the VM, so only call the API if pause-on-sleep is enabled
					// this is useful even on arm64 because IOKit's sleep/wake events are higher-level and closely follow lid close/open events. notably, dark wakes and wake-on-LAN don't cause IOKit to report a wake event to us. so this must be configurable in order to support wake-on-LAN server use cases, but for 99% of users it saves power during sleep because high-CPU-usage containers won't burn CPU during dark wakes or other maintenance wakes
					// to debug: `sudo pmset -g log`
					if vmconfig.Get().Power_PauseOnSleep {
						// wait for 60s before we pause the VM
						// if the lid is closed for less than ~30s and there is a broken app not responding to IOKit notifications, then it may take up to 30s for a wake event to arrive
						// TODO: ideally this would be a timer with timebase=mach_continuous_time/CLOCK_BOOTTIME so that it fires immediately on the next wakeup, instead of requiring 60s of awake time. but Go only has timebase=mach_absolute_time/CLOCK_MONOTONIC timers
						pauseDebounce.Call()
					}
				} else {
					logrus.Info("wake")

					// cancel any pending pause, and make sure we send the wake event after sleep is sent (if it was already in progress)
					pauseDebounce.CancelAndWait()

					// notify VM of wake
					err := vc.Post("sys/wake", nil, nil)
					if err != nil {
						logrus.WithError(err).Error("failed to notify VM of wakeup")
					}
				}
			}
		}
	}()

	return nil
}

func matchTimeoutError(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, unix.ETIMEDOUT) ||
		strings.Contains(err.Error(), "operation timed out") /* tcpip.ErrTimeout */
}

func (vc *VClient) healthCheck() {
	awakeBefore := !iokit.IsAsleep()
	err := vc.DoCheckin()
	if err != nil {
		//TODO require multiple failures
		logrus.WithError(err).Error("health check failed")

		// if it was because of a timeout, then we should sample stacks. vm is dead
		// but only if awake before AND after check, and not recently slept
		if matchTimeoutError(err) &&
			awakeBefore &&
			!iokit.IsAsleep() &&
			!iokit.SleepOrWakeWithin(healthCheckSleepWakeGracePeriod) {
			// too many false positives to stop on health check failures, so disable it for now
			if stopOnHealthCheckFail {
				vc.requestStopCh <- types.StopRequest{Type: types.StopTypeForce, Reason: types.StopReasonHealthCheck}
			}

			// ... but always sample stacks to get debug info in case there's a hang
			go debugutil.SampleStacks(vc.vm)
		}
	}
}

// for CPU, we combine healthcheck with stats report
func (vc *VClient) DoCheckin() error {
	if iokit.IsAsleep() {
		return nil
	}

	stats, err := GetDiskStats(vc.dataFile)
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	if stats != vc.lastStats {
		logrus.Debug("report stats:", stats)
		err := vc.Post("disk/report_stats", stats, nil)
		if err != nil {
			// do NOT wrap. need to match net error for timeout
			return err
		}
	} else {
		logrus.Debug("stats unchanged, not reporting")
	}

	vc.lastStats = stats
	return nil
}

func (vc *VClient) Shutdown() error {
	err := vc.Post("sys/shutdown", nil, nil)
	return err
}

func (vc *VClient) Close() error {
	// close OK: used to signal select loop
	close(vc.signalStopCh)

	vc.VinitClient.Close()
	vc.dataFile.Close()
	return nil
}

func GetDiskStats(imgFile *os.File) (HostDiskStats, error) {
	var statFs unix.Statfs_t
	err := unix.Fstatfs(int(imgFile.Fd()), &statFs)
	if err != nil {
		return HostDiskStats{}, fmt.Errorf("statfs: %w", err)
	}

	var imgStat unix.Stat_t
	err = unix.Fstat(int(imgFile.Fd()), &imgStat)
	if err != nil {
		return HostDiskStats{}, fmt.Errorf("fstat: %w", err)
	}

	return HostDiskStats{
		// excl. reserved blocks
		HostFsFree: statFs.Bavail * uint64(statFs.Bsize),
		// size is apparent, we want real
		DataImgSize: uint64(imgStat.Blocks) * 512,
	}, nil
}
