package vclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vclient/iokit"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	hcsrv "github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol"
	"github.com/orbstack/macvirt/vmgr/vzf"
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

	// arm: arch timer doesn't advance in sleep, so not needed
	// x86: tsc advances in sleep; pausing and resuming prevents that, so monotonic clock and timeouts work as expected, and we don't get stalls
	// but x86 pause/resume is too unstable. it fixes clock, but even on arm64 pausing causes
	// nfs timeouts during sleep (in ~2 min with vsock and hours with tcp)
	// TODO figure out how to make pausing work
	needsPauseResume = false
)

type VClient struct {
	client    *http.Client
	lastStats diskReportStats
	dataFile  *os.File
	vm        *vzf.Machine
	stopChan  chan struct{}

	hcontrolServer *hcsrv.HcontrolServer
}

type diskReportStats struct {
	HostFsSize  uint64 `json:"hostFsSize"`
	HostFsFree  uint64 `json:"hostFsFree"`
	DataImgSize uint64 `json:"dataImgSize"`
}

func newWithTransport(tr *http.Transport, vm *vzf.Machine, hcServer *hcsrv.HcontrolServer) (*VClient, error) {
	httpClient := &http.Client{Transport: tr}
	dataFile, err := os.OpenFile(conf.DataImage(), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &VClient{
		client:         httpClient,
		dataFile:       dataFile,
		vm:             vm,
		stopChan:       make(chan struct{}),
		hcontrolServer: hcServer,
	}, nil
}

func NewWithNetwork(n *vnet.Network, vm *vzf.Machine, hcServer *hcsrv.HcontrolServer) (*VClient, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return gonet.DialContextTCP(ctx, n.Stack, tcpip.FullAddress{
				Addr: n.GuestAddr4,
				Port: ports.GuestVcontrol,
			}, ipv4.ProtocolNumber)
		},
		MaxIdleConns: 2,
	}
	return newWithTransport(tr, vm, hcServer)
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

func (vc *VClient) Post(endpoint string, body any) (*http.Response, error) {
	msg, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	reader := bytes.NewReader(msg)
	req, err := http.NewRequest("POST", baseUrl+"/"+endpoint, reader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := vc.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}

func (vc *VClient) StartBackground() error {
	mon, err := iokit.MonitorSleepWake()
	if err != nil {
		return fmt.Errorf("register iokit: %w", err)
	}

	// don't want to miss the first report, or we'll have to wait
	go func() {
		vc.hcontrolServer.InternalWaitDataFsReady()
		vc.reportDiskStats()
	}()

	// Report disk stats periodically, sync time on wake
	go func() {
		ticker := time.NewTicker(diskStatsInterval)
		defer ticker.Stop()

		for {
			select {
			case <-vc.stopChan:
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
					_, err := vc.Post("sys/wake", nil)
					if err != nil {
						logrus.WithError(err).Error("failed to notify VM of wakeup")
					}
				}()

			case <-ticker.C:
				vc.reportDiskStats()
			}
		}
	}()

	return nil
}

func (vc *VClient) reportDiskStats() {
	var statFs unix.Statfs_t
	err := unix.Fstatfs(int(vc.dataFile.Fd()), &statFs)
	if err != nil {
		logrus.WithError(err).Error("statfs failed")
		return
	}

	var imgStat unix.Stat_t
	err = unix.Fstat(int(vc.dataFile.Fd()), &imgStat)
	if err != nil {
		logrus.WithError(err).Error("stat failed")
		return
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
		_, err := vc.Post("disk/report_stats", stats)
		if err != nil {
			logrus.WithError(err).Error("report stats err")
		}
	} else {
		logrus.Debug("stats unchanged, not reporting")
	}

	vc.lastStats = stats
}

func (vc *VClient) Shutdown() error {
	_, err := vc.Post("sys/shutdown", nil)
	return err
}

func (vc *VClient) Close() error {
	vc.client.CloseIdleConnections()
	vc.dataFile.Close()
	close(vc.stopChan)
	return nil
}
