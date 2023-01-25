package agent

import (
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

	// err doesn't matter, should already be dead from container stop
	_ = c.process.Kill()
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

func (c *Client) SpawnProcess(args SpawnProcessArgs, stdin, stdout, stderr *os.File) (*PidfdProcess, error) {
	// send 3 fds
	err := c.fdx.SendFile(stdin)
	if err != nil {
		return nil, err
	}
	err = c.fdx.SendFile(stdout)
	if err != nil {
		return nil, err
	}
	err = c.fdx.SendFile(stderr)
	if err != nil {
		return nil, err
	}

	var pid int
	err = c.rpc.Call("a.SpawnProcess", args, &pid)
	if err != nil {
		return nil, err
	}

	// recv 1 fd
	pidF, err := c.fdx.RecvFile()
	if err != nil {
		return nil, err
	}

	return wrapPidfdProcess(pidF, pid, c), nil
}

func (c *Client) WaitPid(pid int) (int, error) {
	var status int
	err := c.rpc.Call("a.WaitPid", pid, &status)
	if err != nil {
		return 0, err
	}

	return status, nil
}
