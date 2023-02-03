package hclient

import (
	"net"
	"net/rpc"
	"os/user"
)

type Client struct {
	rpc            *rpc.Client
	user           *user.User
	sshAgentSocket *string
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

func (c *Client) GetUser() (*user.User, error) {
	if c.user != nil {
		return c.user, nil
	}

	var u user.User
	err := c.rpc.Call("hc.GetUser", None{}, &u)
	if err != nil {
		return &user.User{}, err
	}

	c.user = &u
	return &u, nil
}

func (c *Client) GetTimezone() (string, error) {
	var tz string
	err := c.rpc.Call("hc.GetTimezone", None{}, &tz)
	if err != nil {
		return "", err
	}

	return tz, nil
}

func (c *Client) GetSSHPublicKey() (string, error) {
	var key string
	err := c.rpc.Call("hc.GetSSHPublicKey", None{}, &key)
	if err != nil {
		return "", err
	}

	return key, nil
}

func (c *Client) GetSSHAgentSocket() (string, error) {
	if c.sshAgentSocket != nil {
		return *c.sshAgentSocket, nil
	}

	var sock string
	err := c.rpc.Call("hc.GetSSHAgentSocket", None{}, &sock)
	if err != nil {
		return "", err
	}

	c.sshAgentSocket = &sock
	return sock, nil
}

func (c *Client) GetGitConfig() (map[string]string, error) {
	var config map[string]string
	err := c.rpc.Call("hc.GetGitConfig", None{}, &config)
	if err != nil {
		return nil, err
	}

	return config, nil
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
