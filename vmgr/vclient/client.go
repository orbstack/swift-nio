package vclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/util/debugutil"
	"github.com/orbstack/macvirt/vmgr/vclient/iokit"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

var (
	baseUrl = "http://vcontrol"
)

const (
	// match chrony ntp polling interval
	diskStatsInterval = 128 * time.Second
	// very liberal to avoid false positive
	requestTimeout                  = 1 * time.Minute
	healthCheckSleepWakeGracePeriod = requestTimeout

	// arm: arch timer doesn't advance in sleep, so not needed
	// x86: tsc advances in sleep; pausing and resuming prevents that, so monotonic clock and timeouts work as expected, and we don't get stalls
	// but x86 pause/resume is too unstable. it fixes clock, but even on arm64 pausing causes
	// nfs timeouts during sleep (in ~2 min with vsock and hours with tcp)
	// TODO figure out how to make pausing work
	needsPauseResume = false

	// TODO: fix health check
	// sometimes it fails during sleep on arm64
	// level=error msg="health check failed" error="Post \"http://vcontrol/disk/report_stats\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)"
	stopOnHealthCheckFail = false
)

type VClient struct {
	client    *http.Client
	lastStats diskReportStats
	dataFile  *os.File
	vm        vmm.Machine

	signalStopCh     chan struct{}
	requestStopCh    chan<- types.StopRequest
	healthCheckReqCh <-chan struct{}
}

type diskReportStats struct {
	HostFsSize  uint64 `json:"hostFsSize"`
	HostFsFree  uint64 `json:"hostFsFree"`
	DataImgSize uint64 `json:"dataImgSize"`
}

func newWithTransport(tr *http.Transport, vm vmm.Machine, requestStopCh chan<- types.StopRequest, healthCheckReqCh <-chan struct{}) (*VClient, error) {
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   requestTimeout,
	}
	dataFile, err := os.OpenFile(conf.DataImage(), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &VClient{
		client:           httpClient,
		dataFile:         dataFile,
		vm:               vm,
		signalStopCh:     make(chan struct{}),
		requestStopCh:    requestStopCh,
		healthCheckReqCh: healthCheckReqCh,
	}, nil
}

func NewWithNetwork(n *vnet.Network, vm vmm.Machine, requestStopCh chan<- types.StopRequest, healthCheckReqCh <-chan struct{}) (*VClient, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return gonet.DialContextTCP(ctx, n.Stack, tcpip.FullAddress{
				Addr: n.GuestAddr4,
				Port: ports.GuestVcontrol,
			}, ipv4.ProtocolNumber)
		},
		MaxIdleConns: 3,
	}
	return newWithTransport(tr, vm, requestStopCh, healthCheckReqCh)
}

func (vc *VClient) Get(endpoint string) (*http.Response, error) {
	req, err := http.NewRequest("GET", baseUrl+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := vc.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}

func (vc *VClient) Post(endpoint string, body any, out any) error {
	msg, err := json.Marshal(body)
	if err != nil {
		return err
	}

	reader := bytes.NewReader(msg)
	req, err := http.NewRequest("POST", baseUrl+"/"+endpoint, reader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := vc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %w", err)
		}
	} else {
		io.Copy(io.Discard, resp.Body)
	}

	return nil
}

func readError(resp *http.Response) error {
	// read error message
	errBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error body: %s (%s)", err, resp.Status)
	}

	return fmt.Errorf("[vc] %s (%s)", string(errBody), resp.Status)
}

func (vc *VClient) StartBackground() error {
	mon, err := iokit.MonitorSleepWake()
	if err != nil {
		return fmt.Errorf("register iokit: %w", err)
	}

	// Report disk stats periodically, sync time on wake
	go func() {
		ticker := time.NewTicker(diskStatsInterval)
		defer ticker.Stop()

		for {
			select {
			case <-vc.signalStopCh:
				return

			case <-mon.SleepChan:
				logrus.Info("sleep")
				// arm doesn't need pause/resume
				if needsPauseResume {
					err := vc.vm.Pause()
					if err != nil {
						logrus.Error("pause err", err)
					}
				}

			case <-mon.WakeChan:
				logrus.Info("wake")
				// arm doesn't need pause/resume
				if needsPauseResume {
					err := vc.vm.Resume()
					if err != nil {
						logrus.Error("resume err", err)
					}
				}
				go func() {
					err := vc.Post("sys/wake", nil, nil)
					if err != nil {
						logrus.WithError(err).Error("failed to notify VM of wakeup")
					}
				}()

			case <-ticker.C:
				vc.healthCheck()

			case <-vc.healthCheckReqCh:
				vc.healthCheck()
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

	var statFs unix.Statfs_t
	err := unix.Fstatfs(int(vc.dataFile.Fd()), &statFs)
	if err != nil {
		return fmt.Errorf("statfs: %w", err)
	}

	var imgStat unix.Stat_t
	err = unix.Fstat(int(vc.dataFile.Fd()), &imgStat)
	if err != nil {
		return fmt.Errorf("fstat: %w", err)
	}

	stats := diskReportStats{
		HostFsSize: statFs.Blocks * uint64(statFs.Bsize),
		// excl. reserved blocks
		HostFsFree: statFs.Bavail * uint64(statFs.Bsize),
		// size is apparent, we want real
		DataImgSize: uint64(imgStat.Blocks) * 512,
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

	vc.client.CloseIdleConnections()
	vc.dataFile.Close()
	return nil
}
