package vclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vclient/iokit"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/gonet"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

var (
	baseUrl = "http://172.30.30.2:" + strconv.Itoa(ports.GuestVcontrol)
)

const (
	diskStatsInterval = 90 * time.Second
	readyPollInterval = 200 * time.Millisecond

	// arm: arch timer doesn't advance in sleep, so not needed
	// x86: tsc advances in sleep; pausing and resuming prevents that, so monotonic clock and timeouts work as expected, and we don't get stalls
	// but x86 pause/resume is too unstable. it fixes clock, but even on arm64 pausing causes
	// nfs timeouts during sleep (in ~2 min with vsock and hours with tcp)
	// TODO figure out how to make pausing work
	needsPauseResume = false
)

type VClient struct {
	client    *http.Client
	ready     bool
	dataReady bool
	lastStats diskReportStats
	dataFile  *os.File
	vm        *vz.VirtualMachine
}

type diskReportStats struct {
	HostFsSize  uint64 `json:"hostFsSize"`
	HostFsFree  uint64 `json:"hostFsFree"`
	DataImgSize uint64 `json:"dataImgSize"`
}

func newWithTransport(tr *http.Transport, vm *vz.VirtualMachine) (*VClient, error) {
	httpClient := &http.Client{Transport: tr}
	dataFile, err := os.OpenFile(conf.DataImage(), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &VClient{
		client:   httpClient,
		dataFile: dataFile,
		vm:       vm,
	}, nil
}

func NewWithNetwork(n *vnet.Network, vm *vz.VirtualMachine) (*VClient, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return gonet.DialContextTCP(ctx, n.Stack, tcpip.FullAddress{
				Addr: n.GuestAddr4,
				Port: ports.GuestVcontrol,
			}, ipv4.ProtocolNumber)
		},
		MaxIdleConns: 2,
	}
	return newWithTransport(tr, vm)
}

func (vc *VClient) Get(endpoint string) (*http.Response, error) {
	req, err := http.NewRequest("GET", baseUrl+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", GetCurrentToken())
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
	req.Header.Set("Authorization", GetCurrentToken())
	resp, err := vc.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}

func (vc *VClient) WaitForReady() {
	if vc.ready {
		return
	}

	for {
		_, err := vc.Get("ping")
		if err == nil {
			break
		}
		time.Sleep(readyPollInterval)
	}

	vc.ready = true
}

func (vc *VClient) WaitForDataReady() {
	if vc.dataReady {
		return
	}

	for {
		_, err := vc.Get("flag/data_resized")
		if err == nil {
			break
		}
		time.Sleep(readyPollInterval)
	}

	logrus.Info("data ready")
	vc.dataReady = true
}

func (vc *VClient) StartBackground() error {
	// Sync time on wake
	mon, err := iokit.MonitorSleepWake()
	if err != nil {
		return err
	}

	go func() {
		for range mon.WakeChan {
			logrus.Info("wake")
			// arm doesn't need pause/resume
			if needsPauseResume {
				err := vc.vm.Resume()
				if err != nil {
					logrus.Error("resume err", err)
				}
			}
			go func() {
				// For some reason, we have to sync *twice* in order for chrony to step the clock after suspend.
				// Running it twice back-to-back doesn't work, and neither does "chronyc makestep"
				_, err := vc.Post("time/sync", nil)
				if err != nil {
					logrus.Error("sync err", err)
				}

				// 2 sec per iburst check * 4 = 8 sec, plus margin
				time.Sleep(10 * time.Second)
				_, err = vc.Post("time/sync", nil)
				if err != nil {
					logrus.Error("sync err", err)
				}
			}()
		}
	}()

	go func() {
		for range mon.SleepChan {
			logrus.Info("sleep")
			// arm doesn't need pause/resume
			if needsPauseResume {
				err := vc.vm.Pause()
				if err != nil {
					logrus.Error("pause err", err)
				}
			}
		}
	}()

	// Report disk stats periodically
	go func() {
		// don't want to miss the first report, or we'll have to wait
		vc.WaitForDataReady()

		ticker := time.NewTicker(diskStatsInterval)
		for ; true; <-ticker.C {
			vc.reportDiskStats()
		}
	}()

	return nil
}

func (vc *VClient) reportDiskStats() {
	var statFs unix.Statfs_t
	err := unix.Fstatfs(int(vc.dataFile.Fd()), &statFs)
	if err != nil {
		logrus.Error("statfs err", err)
		return
	}

	var imgStat unix.Stat_t
	err = unix.Fstat(int(vc.dataFile.Fd()), &imgStat)
	if err != nil {
		logrus.Error("stat err", err)
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
	return nil
}
