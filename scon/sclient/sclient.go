package sclient

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/netx"
)

const ConnectTimeout = 15 * time.Second

type SconClient struct {
	rpc *jrpc2.Client
}

var noResult interface{}

func New(network, addr string) (*SconClient, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns: 2,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return netx.Dial(network, addr)
			},
		},
	}

	ch := jhttp.NewChannel("http://sconrpc", &jhttp.ChannelOptions{
		Client: httpClient,
	})
	rpcClient := jrpc2.NewClient(ch, nil)
	return &SconClient{
		rpc: rpcClient,
	}, nil
}

func (c *SconClient) Close() error {
	return c.rpc.Close()
}

func (c *SconClient) Ping() error {
	return c.rpc.CallResult(context.TODO(), "Ping", nil, &noResult)
}

func (c *SconClient) Create(req types.CreateRequest) (*types.ContainerRecord, error) {
	var rec types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "Create", req, &rec)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (c *SconClient) ImportContainerFromHostPath(req types.ImportContainerFromHostPathRequest) (*types.ContainerRecord, error) {
	var rec types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "ImportContainerFromHostPath", req, &rec)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (c *SconClient) ListContainers() ([]types.ContainerInfo, error) {
	var records []types.ContainerInfo
	err := c.rpc.CallResult(context.TODO(), "ListContainers", nil, &records)
	if err != nil {
		return nil, err
	}

	return records, nil
}

func (c *SconClient) GetByKey(key string) (*types.ContainerInfo, error) {
	var rec types.ContainerInfo
	err := c.rpc.CallResult(context.TODO(), "GetByKey", types.GenericContainerRequest{
		Key: key,
	}, &rec)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (c *SconClient) GetDefaultContainer() (*types.ContainerRecord, error) {
	var rec types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "GetDefaultContainer", nil, &rec)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (c *SconClient) SetDefaultContainer(key string) error {
	return c.rpc.CallResult(context.TODO(), "SetDefaultContainer", types.GenericContainerRequest{
		Key: key,
	}, &noResult)
}

func (c *SconClient) ContainerStart(key string) error {
	return c.rpc.CallResult(context.TODO(), "ContainerStart", types.GenericContainerRequest{
		Key: key,
	}, &noResult)
}

func (c *SconClient) ContainerStop(key string) error {
	return c.rpc.CallResult(context.TODO(), "ContainerStop", types.GenericContainerRequest{
		Key: key,
	}, &noResult)
}

func (c *SconClient) ContainerRestart(key string) error {
	return c.rpc.CallResult(context.TODO(), "ContainerRestart", types.GenericContainerRequest{
		Key: key,
	}, &noResult)
}

func (c *SconClient) ContainerDelete(key string) error {
	return c.rpc.CallResult(context.TODO(), "ContainerDelete", types.GenericContainerRequest{
		Key: key,
	}, &noResult)
}

func (c *SconClient) ContainerClone(key string, newName string) (*types.ContainerRecord, error) {
	var rec types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "ContainerClone", types.ContainerCloneRequest{
		ContainerKey: key,
		NewName:      newName,
	}, &rec)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (c *SconClient) ContainerRename(key string, newName string) error {
	return c.rpc.CallResult(context.TODO(), "ContainerRename", types.ContainerRenameRequest{
		ContainerKey: key,
		NewName:      newName,
	}, &noResult)
}

func (c *SconClient) ContainerGetLogs(key string, logType types.LogType) (string, error) {
	var logs string
	err := c.rpc.CallResult(context.TODO(), "ContainerGetLogs", types.ContainerGetLogsRequest{
		ContainerKey: key,
		Type:         logType,
	}, &logs)
	if err != nil {
		return "", err
	}

	return logs, nil
}

func (c *SconClient) ContainerSetConfig(key string, config types.MachineConfig) error {
	return c.rpc.CallResult(context.TODO(), "ContainerSetConfig", types.ContainerSetConfigRequest{
		ContainerKey: key,
		Config:       config,
	}, &noResult)
}

func (c *SconClient) ContainerExportToHostPath(key string, hostPath string) error {
	return c.rpc.CallResult(context.TODO(), "ContainerExportToHostPath", types.ContainerExportRequest{
		ContainerKey: key,
		HostPath:     hostPath,
	}, &noResult)
}

func (c *SconClient) WormholeNukeData() error {
	return c.rpc.CallResult(context.TODO(), "WormholeNukeData", nil, &noResult)
}

func (c *SconClient) InternalReportStopped(id string) error {
	return c.rpc.CallResult(context.TODO(), "InternalReportStopped", types.InternalReportStoppedRequest{
		ID: id,
	}, &noResult)
}

func (c *SconClient) InternalDockerMigrationLoadImage(req types.InternalDockerMigrationLoadImageRequest) error {
	return c.rpc.CallResult(context.TODO(), "InternalDockerMigrationLoadImage", req, &noResult)
}

func (c *SconClient) InternalDockerMigrationRunSyncServer(req types.InternalDockerMigrationRunSyncServerRequest) error {
	return c.rpc.CallResult(context.TODO(), "InternalDockerMigrationRunSyncServer", req, &noResult)
}

func (c *SconClient) InternalDockerMigrationWaitSync(req types.InternalDockerMigrationWaitSyncRequest) error {
	return c.rpc.CallResult(context.TODO(), "InternalDockerMigrationWaitSync", req, &noResult)
}

func (c *SconClient) InternalDockerMigrationStopSyncServer() error {
	return c.rpc.CallResult(context.TODO(), "InternalDockerMigrationStopSyncServer", nil, &noResult)
}

func (c *SconClient) InternalDeleteK8s() error {
	return c.rpc.CallResult(context.TODO(), "InternalDeleteK8s", nil, &noResult)
}

func (c *SconClient) InternalGuiReportStarted() error {
	return c.rpc.CallResult(context.TODO(), "InternalGuiReportStarted", nil, &noResult)
}

func (c *SconClient) InternalDockerExportVolumeToHostPath(req types.InternalDockerExportVolumeToHostPathRequest) error {
	return c.rpc.CallResult(context.TODO(), "InternalDockerExportVolumeToHostPath", req, &noResult)
}

func (c *SconClient) InternalDockerImportVolumeFromHostPath(req types.InternalDockerImportVolumeFromHostPathRequest) error {
	return c.rpc.CallResult(context.TODO(), "InternalDockerImportVolumeFromHostPath", req, &noResult)
}
