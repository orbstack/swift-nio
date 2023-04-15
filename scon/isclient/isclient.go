package isclient

import (
	"net"
	"net/rpc"

	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/scon/isclient/istypes"
)

type None struct{}

type Client struct {
	rpc *rpc.Client
}

func New(conn net.Conn) (*Client, error) {
	return &Client{
		rpc: rpc.NewClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.rpc.Close()
}

func (c *Client) Ping() error {
	var noResult None
	return c.rpc.Call("sci.Ping", None{}, &noResult)
}

func (c *Client) OnDrmResult(result *drmtypes.Result) error {
	var noResult None
	return c.rpc.Call("sci.OnDrmResult", result, &noResult)
}

func (c *Client) OnNfsMounted() error {
	var noResult None
	return c.rpc.Call("sci.OnNfsMounted", None{}, &noResult)
}

func (c *Client) InjectFsnotifyEvents(events istypes.FsnotifyEventsBatch) error {
	var noResult None
	return c.rpc.Call("sci.InjectFsnotifyEvents", events, &noResult)
}
