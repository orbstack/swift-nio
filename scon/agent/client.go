package agent

import (
	"errors"
	"net"
	"net/rpc"
	"os"
	"sync/atomic"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"golang.org/x/sys/unix"
)

const (
	stopWarningSignal = unix.SIGPWR
)

type Client struct {
	// process before double fork
	initialProcess *os.Process
	// pidfd of real agent process, after double fork
	// CAREFUL: agent is nil so we can't WaitStatus on it, only signal
	process atomic.Pointer[PidfdProcess]

	rpc *rpc.Client
	fdx *Fdx
}

func NewClient(initialProcess *os.Process, rpcConn net.Conn, fdxConn net.Conn) *Client {
	return &Client{
		initialProcess: initialProcess,
		rpc:            rpc.NewClient(rpcConn),
		fdx:            NewFdx(fdxConn),
	}
}

func (c *Client) Fdx() *Fdx {
	return c.fdx
}

func (c *Client) Close() error {
	c.rpc.Close()
	c.fdx.Close()

	// err doesn't matter, should already be dead from container stop
	_ = c.initialProcess.Kill()
	process := c.process.Load()
	if process != nil {
		_ = process.Kill()
		process.Release()
	}
	return nil
}

func (c *Client) Ping() error {
	var none None
	return c.rpc.Call("a.Ping", none, &none)
}

func (c *Client) StartProxyTCP(spec ProxySpec, listener net.Listener) (ProxyResult, error) {
	// send fd
	file, err := listener.(*netx.TCPListener).File()
	if err != nil {
		return ProxyResult{}, err
	}
	defer file.Close() // this is a dup

	seq, err := c.fdx.SendFile(file)
	if err != nil {
		return ProxyResult{}, err
	}

	var reply ProxyResult
	err = c.rpc.Call("a.StartProxyTCP", StartProxyArgs{
		ProxySpec: spec,
		FdxSeq:    seq,
	}, &reply)
	if err != nil {
		return ProxyResult{}, err
	}

	return reply, nil
}

func (c *Client) StartProxyUDP(spec ProxySpec, listener net.Conn) (ProxyResult, error) {
	// send fd
	file, err := listener.(*net.UDPConn).File()
	if err != nil {
		return ProxyResult{}, err
	}
	defer file.Close() // this is a dup

	seq, err := c.fdx.SendFile(file)
	if err != nil {
		return ProxyResult{}, err
	}

	var reply ProxyResult
	err = c.rpc.Call("a.StartProxyUDP", StartProxyArgs{
		ProxySpec: spec,
		FdxSeq:    seq,
	}, &reply)
	if err != nil {
		return ProxyResult{}, err
	}

	return reply, nil
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

func (c *Client) DockerDialSocket() (net.Conn, error) {
	var seq uint64
	err := c.rpc.Call("a.DockerDialSocket", None{}, &seq)
	if err != nil {
		return nil, err
	}

	file, err := c.fdx.RecvFile(seq)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return net.FileConn(file)
}

func (c *Client) DockerSyncEvents() error {
	var none None
	err := c.rpc.Call("a.DockerSyncEvents", None{}, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerCheckIdle() (bool, error) {
	var idle bool
	err := c.rpc.Call("a.DockerCheckIdle", None{}, &idle)
	if err != nil {
		return false, err
	}

	return idle, nil
}

func (c *Client) DockerWaitStart() error {
	var none None
	err := c.rpc.Call("a.DockerWaitStart", None{}, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerQueryKubeDns(q dns.Question) ([]dns.RR, error) {
	var reply []dns.RR
	err := c.rpc.Call("a.DockerQueryKubeDns", q, &reply)
	if err != nil {
		return nil, err
	}

	return reply, nil
}

func (c *Client) DockerMigrationLoadImage(params types.InternalDockerMigrationLoadImageRequest) error {
	var none None
	err := c.rpc.Call("a.DockerMigrationLoadImage", params, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerMigrationRunSyncServer(params types.InternalDockerMigrationRunSyncServerRequest) error {
	var none None
	err := c.rpc.Call("a.DockerMigrationRunSyncServer", params, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerMigrationWaitSync(params types.InternalDockerMigrationWaitSyncRequest) error {
	var none None
	err := c.rpc.Call("a.DockerMigrationWaitSync", params, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerMigrationStopSyncServer() error {
	var none None
	err := c.rpc.Call("a.DockerMigrationStopSyncServer", None{}, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerGuiReportStarted() error {
	var none None
	err := c.rpc.Call("a.DockerGuiReportStarted", None{}, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerOnVmconfigUpdate(config *vmconfig.VmConfig) error {
	var none None
	err := c.rpc.Call("a.DockerOnVmconfigUpdate", config, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerFastDf() (*dockertypes.SystemDf, error) {
	var df dockertypes.SystemDf
	err := c.rpc.Call("a.DockerFastDf", None{}, &df)
	if err != nil {
		return nil, err
	}

	return &df, nil
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

func (c *Client) DialTCPContext(addrPort string) (*net.TCPConn, error) {
	var seq uint64
	err := c.rpc.Call("a.DialTCPContext", DialTCPContextArgs{
		AddrPort: addrPort,
	}, &seq)
	if err != nil {
		return nil, err
	}

	file, err := c.fdx.RecvFile(seq)
	if err != nil {
		return nil, err
	}

	conn, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}

	return conn.(*net.TCPConn), nil
}

func (c *Client) EndUserSession(user string) error {
	var none None
	err := c.rpc.Call("a.EndUserSession", user, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) GetAgentPidFd() error {
	if c.process.Load() != nil {
		return errors.New("agent pidfd already set")
	}

	var seq uint64
	err := c.rpc.Call("a.GetAgentPidFd", None{}, &seq)
	if err != nil {
		return err
	}

	file, err := c.fdx.RecvFile(seq)
	if err != nil {
		return err
	}

	// update own process reference
	// agent=nil: doesn't make sense to ask agent to wait on itself
	c.process.Store(wrapPidfdProcess(file, 0, nil))
	return nil
}

func (c *Client) SyntheticWarnStop() error {
	process := c.process.Load()
	if process == nil {
		return errors.New("no agent pidfd process")
	}

	return process.Signal(stopWarningSignal)
}

type UpdateHostnameArgs struct {
	OldName string
	NewName string
}

func (c *Client) UpdateHostname(oldName string, newName string) error {
	var none None
	err := c.rpc.Call("a.UpdateHostname", UpdateHostnameArgs{
		OldName: oldName,
		NewName: newName,
	}, &none)
	if err != nil {
		return err
	}

	return nil
}
