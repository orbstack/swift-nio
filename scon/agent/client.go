package agent

import (
	"errors"
	"net"
	"net/rpc"
	"os"
)

type Client struct {
	process *os.Process
	rpc     *rpc.Client
	fdx     *Fdx
}

func NewClient(process *os.Process, rpcConn net.Conn, fdxConn net.Conn) *Client {
	return &Client{
		process: process,
		rpc:     rpc.NewClient(rpcConn),
		fdx:     NewFdx(fdxConn),
	}
}

func (c *Client) Close() error {
	c.rpc.Close()
	c.fdx.Close()

	err := c.process.Kill()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	return nil
}

func (c *Client) Ping() error {
	var none None
	return c.rpc.Call("a.Ping", none, &none)
}

func (c *Client) GetListeners() ([]ProcListener, error) {
	var none None
	var listeners []ProcListener
	err := c.rpc.Call("a.GetListeners", none, &listeners)
	if err != nil {
		return nil, err
	}

	return listeners, nil
}

func (c *Client) OpenDiagNetlink() (*os.File, error) {
	var none None
	err := c.rpc.Call("a.OpenDiagNetlink", none, &none)
	if err != nil {
		return nil, err
	}

	return c.fdx.RecvFile()
}

func (c *Client) StartProxyTCP(spec ProxySpec, listener net.Listener) error {
	// send fd
	file, err := listener.(*net.TCPListener).File()
	if err != nil {
		return err
	}
	defer file.Close() // this is a dup

	err = c.fdx.SendFile(file)
	if err != nil {
		return err
	}

	var none None
	err = c.rpc.Call("a.StartProxyTCP", spec, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) StartProxyUDP(spec ProxySpec, listener net.Conn) error {
	// send fd
	file, err := listener.(*net.UDPConn).File()
	if err != nil {
		return err
	}
	defer file.Close() // this is a dup

	err = c.fdx.SendFile(file)
	if err != nil {
		return err
	}

	var none None
	err = c.rpc.Call("a.StartProxyUDP", spec, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) StopProxyTCP(spec ProxySpec) error {
	var none None
	err := c.rpc.Call("a.StopProxyTCP", spec, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) StopProxyUDP(spec ProxySpec) error {
	var none None
	err := c.rpc.Call("a.StopProxyUDP", spec, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) InitialSetup(args InitialSetupArgs) error {
	var none None
	err := c.rpc.Call("a.InitialSetup", args, &none)
	if err != nil {
		return err
	}

	return nil
}
