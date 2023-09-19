package bugreport

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/appapi"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type ReportPackage struct {
	Name string
	Data []byte
}

func addSconParts(r *ReportWriter) {
	// this part may panic if VM is locked up (panic: timed out waiting for services)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			logrus.WithError(err).Error("failed to get scon info")
		}
	}()

	// try to get machine logs from vmgr:
	// list all machines, then get all logs for each machine
	containers, err := scli.Client().ListContainers()
	if err != nil {
		logrus.WithError(err).Error("failed to list containers")
	} else {
		// add container list
		err = r.AddFileJson("machines.json", containers)
		if err != nil {
			logrus.WithError(err).Error("failed to add machines.json")
		}
	}

	// add machine logs
	for _, c := range containers {
		logTxt, err := scli.Client().ContainerGetLogs(&c, types.LogConsole)
		if err != nil {
			logrus.WithError(err).Errorf("failed to get logs for %s", c.Name)
		} else {
			err = r.addFileBytes(fmt.Sprintf("machine_logs/%s.%s.console.log", c.Name, c.ID), []byte(logTxt))
			if err != nil {
				logrus.WithError(err).Errorf("failed to add logs for %s", c.Name)
			}
		}

		logTxt, err = scli.Client().ContainerGetLogs(&c, types.LogRuntime)
		if err != nil {
			logrus.WithError(err).Errorf("failed to get logs for %s", c.Name)
		} else {
			err = r.addFileBytes(fmt.Sprintf("machine_logs/%s.%s.runtime.log", c.Name, c.ID), []byte(logTxt))
			if err != nil {
				logrus.WithError(err).Errorf("failed to add logs for %s", c.Name)
			}
		}
	}
}

func BuildZip(infoTxt []byte) (*ReportPackage, error) {
	// start zip
	r := newReport()

	// add info.txt
	err := r.addFileBytes("info.txt", infoTxt)
	if err != nil {
		return nil, err
	}

	// add OrbStack configs: vmconfig, docker daemon
	err = r.AddFileLocal(coredir.VmConfigFile(), "config/vmconfig.json")
	if err != nil {
		logrus.WithError(err).Error("failed to add vmconfig.json")
	}
	err = r.AddFileLocal(conf.DockerDaemonConfig(), "config/docker.json")
	if err != nil {
		logrus.WithError(err).Error("failed to add docker.json")
	}

	// add vmgr logs
	err = r.AddDirLocal(conf.LogDir(), "vmgr_logs")
	if err != nil {
		logrus.WithError(err).Error("failed to add vmgr logs")
	}

	if vmclient.IsRunning() {
		addSconParts(r)
	}

	// add netstat -rn
	netstat, err := util.RunWithOutput("netstat", "-rn")
	if err != nil {
		logrus.WithError(err).Error("failed to get netstat -rn")
	} else {
		err = r.addFileBytes("netstat_rn.txt", []byte(netstat))
		if err != nil {
			logrus.WithError(err).Error("failed to add netstat -rn")
		}
	}

	// add statfs and stat
	var statfs unix.Statfs_t
	err = unix.Statfs(conf.DataDir(), &statfs)
	if err != nil {
		logrus.WithError(err).Error("failed to get statfs")
	} else {
		err = r.AddFileJson("statfs.json", statfs)
		if err != nil {
			logrus.WithError(err).Error("failed to add statfs")
		}
	}
	var imgStat unix.Stat_t
	err = unix.Stat(conf.DataImage(), &imgStat)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logrus.WithError(err).Error("failed to get stat")
		}
	} else {
		err = r.AddFileJson("stat_dataimg.json", imgStat)
		if err != nil {
			logrus.WithError(err).Error("failed to add stat")
		}
	}

	return r.Finish()
}

func (r *ReportPackage) getPresignedURL(client *appapi.Client) (*drmtypes.UploadDiagReportResponse, error) {
	uploadReq := drmtypes.UploadDiagReportRequest{
		Name: r.Name,
		Size: int64(len(r.Data)),
	}

	var uploadResp drmtypes.UploadDiagReportResponse
	err := client.Post("/debug/diag_reports", uploadReq, &uploadResp)
	if err != nil {
		return nil, fmt.Errorf("get presigned url: %w", err)
	}

	return &uploadResp, nil
}

func (r *ReportPackage) uploadToURL(client *appapi.Client, uploadURL string) error {
	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(r.Data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(r.Data)))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to upload: %s", resp.Status)
	}

	return nil
}

func (r *ReportPackage) Upload() (string, error) {
	client := appapi.NewClient()

	// get a presigned url
	resp, err := r.getPresignedURL(client)
	if err != nil {
		return "", err
	}

	// upload to url
	err = r.uploadToURL(client, resp.UploadURL)
	if err != nil {
		return "", err
	}

	return resp.DownloadURL, nil
}

func BuildAndUpload(infoTxt []byte) (string, error) {
	pkg, err := BuildZip(infoTxt)
	if err != nil {
		return "", err
	}

	// clear saved dir
	err = os.RemoveAll(conf.DiagDir())
	if err != nil {
		return "", err
	}

	// save to disk
	err = os.MkdirAll(conf.DiagDir(), 0755)
	if err != nil {
		return "", err
	}
	err = os.WriteFile(conf.DiagDir()+"/"+pkg.Name, pkg.Data, 0644)
	if err != nil {
		return "", err
	}

	downloadURL, err := pkg.Upload()
	if err != nil {
		return "", err
	}

	return downloadURL, nil
}
