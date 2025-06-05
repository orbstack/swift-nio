package agent

import (
	"fmt"
	"net"
	"net/rpc"
	"os"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/scon/util/sysx"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"golang.org/x/sys/unix"
)

type Client struct {
	rpc     *rpc.Client
	rpcConn net.Conn
	fdx     *Fdx
}

func NewClient(rpcConn net.Conn, fdxConn net.Conn) *Client {
	return &Client{
		rpc:     rpc.NewClient(rpcConn),
		rpcConn: rpcConn,
		fdx:     NewFdx(fdxConn),
	}
}

func (c *Client) SyntheticWaitForClose() error {
	conn := c.rpcConn.(*net.UnixConn)

	// to avoid blocking Close() (due to pfd refcount), use a copy of the fd
	file, err := conn.File()
	if err != nil {
		return err
	}
	defer file.Close()

	rawConn, err := file.SyscallConn()
	if err != nil {
		return err
	}

	for {
		// wait for close
		closed, err := util.UseRawConn1(rawConn, func(fd int) (bool, error) {
			revents, err := sysx.PollFd(fd, unix.POLLHUP)
			if err != nil {
				return false, err
			}
			if revents&unix.POLLHUP != 0 {
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return err
		}
		if closed {
			return nil
		}
	}
}

func (c *Client) Fdx() *Fdx {
	return c.fdx
}

func (c *Client) Close() error {
	c.rpc.Close()
	c.fdx.Close()
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

func (c *Client) InitialSetupStage1(args InitialSetupArgs) error {
	var none None
	err := c.rpc.Call("a.InitialSetupStage1", args, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) InitialSetupStage2(args InitialSetupArgs) error {
	var none None
	err := c.rpc.Call("a.InitialSetupStage2", args, &none)
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

func (c *Client) SpawnProcess(args SpawnProcessArgs, childFiles []*os.File) (*PidfdProcess, error) {
	// send fds
	seq, err := c.fdx.SendFiles(childFiles...)
	if err != nil {
		return nil, fmt.Errorf("send files: %w", err)
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

func (c *Client) DockerDialRealSocket() (net.Conn, error) {
	var seq uint64
	err := c.rpc.Call("a.DockerDialRealSocket", None{}, &seq)
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

func (c *Client) DockerAddCertsToContainer(containerID string) error {
	var none None
	err := c.rpc.Call("a.DockerAddCertsToContainer", containerID, &none)
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

func (c *Client) DockerOnStop() error {
	var none None
	err := c.rpc.Call("a.DockerOnStop", None{}, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerMigrationLoadImage(params types.InternalDockerMigrationLoadImageRequest, remoteConn *net.TCPConn) error {
	file, err := remoteConn.File()
	if err != nil {
		return err
	}
	defer file.Close()

	seq, err := c.fdx.SendFile(file)
	if err != nil {
		return err
	}
	params.RemoteConnFdxSeq = seq

	var none None
	err = c.rpc.Call("a.DockerMigrationLoadImage", params, &none)
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

func (c *Client) DockerOnVmconfigUpdate(config *vmtypes.VmConfig) error {
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

func (c *Client) DockerExportVolumeToHostPath(args types.InternalDockerExportVolumeToHostPathRequest) error {
	var none None
	err := c.rpc.Call("a.DockerExportVolumeToHostPath", args, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerImportVolumeFromHostPath(args types.InternalDockerImportVolumeFromHostPathRequest) error {
	var none None
	err := c.rpc.Call("a.DockerImportVolumeFromHostPath", args, &none)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) DockerStartWormhole(args StartWormholeArgs) (*StartWormholeResponseClient, error) {
	var reply StartWormholeResponse
	err := c.rpc.Call("a.DockerStartWormhole", StartWormholeArgs{
		Target: args.Target,
	}, &reply)
	if err != nil {
		return nil, err
	}

	files, err := c.fdx.RecvFiles(reply.FdxSeq)
	if err != nil {
		return nil, err
	}

	resp := &StartWormholeResponseClient{
		StartWormholeResponse: reply,
		InitPidfdFile:         files[0],
		RootfsFile:            files[1],
	}
	if len(files) >= 3 {
		resp.FanotifyFile = files[2]
	}

	return resp, nil
}

func (c *Client) DockerEndWormhole(args EndWormholeArgs) error {
	var none None
	err := c.rpc.Call("a.DockerEndWormhole", args, &none)
	if err != nil {
		return err
	}

	return nil
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

func (c *Client) DialContext(network string, addrPort string) (net.Conn, error) {
	var seq uint64
	err := c.rpc.Call("a.DialContext", DialContextArgs{
		Network:  network,
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

	return conn, nil
}

func (c *Client) EndUserSession(user string) error {
	var none None
	err := c.rpc.Call("a.EndUserSession", user, &none)
	if err != nil {
		return err
	}

	return nil
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

func (c *Client) DockerOnContainerPreStart(cid string) error {
	var none None
	err := c.rpc.Call("a.DockerOnContainerPreStart", cid, &none)
	if err != nil {
		return err
	}

	return nil
}
