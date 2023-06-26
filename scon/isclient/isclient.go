package isclient

import (
	"net"
	"net/rpc"

	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
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
