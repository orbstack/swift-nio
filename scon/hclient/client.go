package hclient

import (
	"net"
	"net/rpc"
)

type Client struct {
	rpc *rpc.Client
}

func (c *Client) Ping() error {
	var none None
	return c.rpc.Call("hc.Ping", none, &none)
}

func (c *Client) StartForward(spec ForwardSpec) error {
	var none None
	return c.rpc.Call("hc.StartForward", spec, &none)
}

func (c *Client) StopForward(spec ForwardSpec) error {
	var none None
	return c.rpc.Call("hc.StopForward", spec, &none)
}

func (c *Client) Close() error {
	return c.rpc.Close()
}

func New(conn net.Conn) (*Client, error) {
	rpcClient := rpc.NewClient(conn)
	return &Client{
		rpc: rpcClient,
	}, nil
}
