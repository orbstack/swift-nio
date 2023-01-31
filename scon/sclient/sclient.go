package sclient

import (
	"context"
	"net"
	"net/http"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/scon/types"
)

type SconClient struct {
	rpc *jrpc2.Client
}

var noResult interface{}

func New(network, addr string) (*SconClient, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns: 5,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial(network, addr)
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

func (c *SconClient) ListContainers() ([]types.ContainerRecord, error) {
	var records []types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "ListContainers", nil, &records)
	if err != nil {
		return nil, err
	}

	return records, nil
}

func (c *SconClient) GetByID(id string) (*types.ContainerRecord, error) {
	var rec types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "GetByID", types.GetByIDRequest{
		ID: id,
	}, &rec)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (c *SconClient) GetByName(name string) (*types.ContainerRecord, error) {
	var rec types.ContainerRecord
	err := c.rpc.CallResult(context.TODO(), "GetByName", types.GetByNameRequest{
		Name: name,
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

func (c *SconClient) ContainerStart(record *types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerStart", record, &noResult)
}

func (c *SconClient) ContainerStop(record *types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerStop", record, &noResult)
}

func (c *SconClient) ContainerDelete(record *types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerDelete", record, &noResult)
}

func (c *SconClient) ContainerFreeze(record *types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerFreeze", record, &noResult)
}

func (c *SconClient) ContainerUnfreeze(record *types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerUnfreeze", record, &noResult)
}

func (c *SconClient) InternalReportStopped(id string) error {
	return c.rpc.CallResult(context.TODO(), "InternalReportStopped", types.InternalReportStoppedRequest{
		ID: id,
	}, nil)
}

func (c *SconClient) StopServerVM() error {
	return c.rpc.CallResult(context.TODO(), "StopServerVM", nil, nil)
}
