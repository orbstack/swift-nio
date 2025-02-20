package isclient

import (
	"net"
	"net/rpc"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
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

func (c *Client) OnVmconfigUpdate(config *vmtypes.VmConfig) error {
	var noResult None
	return c.rpc.Call("sci.OnVmconfigUpdate", config, &noResult)
}

func (c *Client) MdnsHandleQuery(q dns.Question) ([]dns.RR, error) {
	var result []dns.RR
	err := c.rpc.Call("sci.MdnsHandleQuery", q, &result)
	return result, err
}
