package agent

import (
	"net"
	"net/rpc"
	"os"
)

type Client struct {
	process      *os.Process
	rpc          *rpc.Client
	fdx          *Fdx
	inetDiagFile *os.File
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
	if c.inetDiagFile != nil {
		c.inetDiagFile.Close()
		c.inetDiagFile = nil
	}

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
	var seq uint64
	err := c.rpc.Call("a.OpenDiagNetlink", None{}, &seq)
	if err != nil {
		return nil, err
	}

	file, err := c.fdx.RecvFile(seq)
	if err != nil {
		return nil, err
	}

	c.inetDiagFile = file
	return file, nil
}

func (c *Client) StartProxyTCP(spec ProxySpec, listener net.Listener) error {
	// send fd
	file, err := listener.(*net.TCPListener).File()
	if err != nil {
		return err
	}
	defer file.Close() // this is a dup

	seq, err := c.fdx.SendFile(file)
	if err != nil {
		return err
	}

	var none None
	err = c.rpc.Call("a.StartProxyTCP", StartProxyArgs{
		ProxySpec: spec,
		FdxSeq:    seq,
	}, &none)
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

	seq, err := c.fdx.SendFile(file)
	if err != nil {
		return err
	}

	var none None
	err = c.rpc.Call("a.StartProxyUDP", StartProxyArgs{
		ProxySpec: spec,
		FdxSeq:    seq,
	}, &none)
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

func (c *Client) ResolveSSHDir(args ResolveSSHDirArgs) (string, error) {
	var dir string
	err := c.rpc.Call("a.ResolveSSHDir", args, &dir)
	if err != nil {
		return "", err
	}

	return dir, nil
}

func (c *Client) SpawnProcess(args SpawnProcessArgs, stdin, stdout, stderr *os.File) (*PidfdProcess, error) {
	// send 3 fds
	seq, err := c.fdx.SendFiles(stdin, stdout, stderr)
	if err != nil {
		return nil, err
	}
	args.FdxSeq = seq

	var reply SpawnProcessReply
	err = c.rpc.Call("a.SpawnProcess", args, &reply)
	if err != nil {
		return nil, err
	}

	// recv 1 fd
	pidF, err := c.fdx.RecvFile(reply.FdxSeq)
	if err != nil {
		return nil, err
	}

	return wrapPidfdProcess(pidF, reply.Pid, c), nil
}

func (c *Client) WaitPid(pid int) (int, error) {
	var status int
	err := c.rpc.Call("a.WaitPid", pid, &status)
	if err != nil {
		return 0, err
	}

	return status, nil
}

func (c *Client) HandleDockerConn(conn net.Conn) error {
	file, err := conn.(*net.TCPConn).File()
	if err != nil {
		return err
	}

	seq, err := c.fdx.SendFile(file)
	file.Close()
	if err != nil {
		return err
	}

	var none None
	err = c.rpc.Call("a.HandleDockerConn", seq, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) CheckDockerIdle() (bool, error) {
	var idle bool
	err := c.rpc.Call("a.CheckDockerIdle", None{}, &idle)
	if err != nil {
		return false, err
	}

	return idle, nil
}

func (c *Client) ServeSftp(user string, socket *os.File) (int, error) {
	seq, err := c.fdx.SendFile(socket)
	if err != nil {
		return 0, err
	}

	var exitCode int
	err = c.rpc.Call("a.ServeSftp", ServeSftpArgs{
		User:   user,
		FdxSeq: seq,
	}, &exitCode)
	if err != nil {
		return 0, err
	}

	return exitCode, nil
}
