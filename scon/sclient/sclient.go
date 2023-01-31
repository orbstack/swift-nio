package sclient

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/jhttp"
	"github.com/kdrag0n/macvirt/scon/types"
)

type SconClient struct {
	rpc *jrpc2.Client
}

func New(url string) (*SconClient, error) {
	ch := jhttp.NewChannel(url, nil)
	rpcClient := jrpc2.NewClient(ch, nil)
	return &SconClient{
		rpc: rpcClient,
	}, nil
}

func (c *SconClient) Close() error {
	return c.rpc.Close()
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

func (c *SconClient) ContainerStart(record types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerStart", record, nil)
}

func (c *SconClient) ContainerStop(record types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerStop", record, nil)
}

func (c *SconClient) ContainerDelete(record types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerDelete", record, nil)
}

func (c *SconClient) ContainerFreeze(record types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerFreeze", record, nil)
}

func (c *SconClient) ContainerUnfreeze(record types.ContainerRecord) error {
	return c.rpc.CallResult(context.TODO(), "ContainerUnfreeze", record, nil)
}

func (c *SconClient) InternalReportStopped(id string) error {
	return c.rpc.CallResult(context.TODO(), "InternalReportStopped", types.InternalReportStoppedRequest{
		ID: id,
	}, nil)
}
