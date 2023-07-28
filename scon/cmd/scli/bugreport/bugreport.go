package bugreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"github.com/sirupsen/logrus"
)

const (
	apiBaseURL = "https://api-misc.orbstack.dev"
	// apiBaseURL = "http://localhost:8400"
)

type ReportPackage struct {
	Name string
	Data []byte
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

	return r.Finish()
}

func (r *ReportPackage) getPresignedURL(httpClient *http.Client) (*drmtypes.UploadDiagReportResponse, error) {
	uploadReq := drmtypes.UploadDiagReportRequest{
		Name: r.Name,
		Size: int64(len(r.Data)),
	}
	uploadReqBytes, err := json.Marshal(uploadReq)
	if err != nil {
		return nil, err
	}
	req, err := httpClient.Post(apiBaseURL+"/api/v1/debug/diag_reports", "application/json", bytes.NewReader(uploadReqBytes))
	if err != nil {
		return nil, err
	}
	defer req.Body.Close()

	if req.StatusCode != 200 {
		return nil, fmt.Errorf("failed to get presigned url: %s", req.Status)
	}

	var uploadResp drmtypes.UploadDiagReportResponse
	err = json.NewDecoder(req.Body).Decode(&uploadResp)
	if err != nil {
		return nil, err
	}

	return &uploadResp, nil
}

func (r *ReportPackage) uploadToURL(httpClient *http.Client, uploadURL string) error {
	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(r.Data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(r.Data)))

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to upload: %s", resp.Status)
	}

	return nil
}

func (r *ReportPackage) Upload() (string, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    3,
			IdleConnTimeout: 60 * time.Second,
			// TODO this may be the wrong proxy
			Proxy: func(req *http.Request) (*url.URL, error) {
				return http.ProxyFromEnvironment(req)
			},
		},
	}

	// get a presigned url
	resp, err := r.getPresignedURL(httpClient)
	if err != nil {
		return "", err
	}

	// upload to url
	err = r.uploadToURL(httpClient, resp.UploadURL)
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

	downloadURL, err := pkg.Upload()
	if err != nil {
		return "", err
	}

	return downloadURL, nil
}
