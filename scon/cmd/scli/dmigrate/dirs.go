package dmigrate

import (
	"bytes"
	"fmt"
	"time"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

const (
	serverPollInterval = 15 * time.Millisecond
	serverStartTimeout = 10 * time.Second
)

func execAs(client *dockerclient.Client, cid string, execReq *dockertypes.ContainerExecCreateRequest) (string, error) {
	var execResp dockertypes.ContainerExecCreateResponse
	err := client.Call("POST", "/containers/"+cid+"/exec", execReq, &execResp)
	if err != nil {
		return "", fmt.Errorf("create exec: %w", err)
	}

	// run the tar
	reader, err := client.StreamRead("POST", "/exec/"+execResp.ID+"/start", dockertypes.ContainerExecStartRequest{
		Detach: false,
	})
	if err != nil {
		return "", fmt.Errorf("start exec: %w", err)
	}
	defer reader.Close()

	var output bytes.Buffer
	err = demuxOutput(reader, &output)
	if err != nil {
		return "", fmt.Errorf("demux output: %w", err)
	}

	// check exec exit status
	var execInspect dockertypes.ContainerExecInspect
	err = client.Call("GET", "/exec/"+execResp.ID+"/json", nil, &execInspect)
	if err != nil {
		return "", fmt.Errorf("inspect exec: %w", err)
	}

	if execInspect.ExitCode != 0 {
		return "", fmt.Errorf("exec exit code: %d; output: %s", execInspect.ExitCode, output.String())
	}

	// success
	return output.String(), nil
}

func (m *Migrator) syncDirs(srcClient *dockerclient.Client, srcs []string, destClient *dockerclient.Client, dest string) error {
	if len(srcs) != 1 {
		return fmt.Errorf("must have exactly 1 src dir")
	}

	cmdBuilder := func(port int) []string {
		return []string{
			"bash",
			"-c",
			// /dev/tcp is raw socket fd
			fmt.Sprintf("set -e; tar --numeric-owner -cf - . > /dev/tcp/host.docker.internal/%d", port),
		}
	}

	return m.syncDirsGeneric(srcClient, cmdBuilder, srcs[0], destClient, dest)
}

func (m *Migrator) syncDirsGeneric(srcClient *dockerclient.Client, cmdBuilder func(int) []string, srcCwd string, destClient *dockerclient.Client, dest string) error {
	// TODO check for conflict on docker machine side
	port, err := findFreeTCPPort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}

	// start call
	destErrC := make(chan error, 1) // buffered in case we exit early
	go func() {
		destErrC <- scli.Client().InternalDockerMigrationSyncDirs(types.InternalDockerMigrationSyncDirsRequest{
			Port: port,
			Dirs: []string{dest},
		})
	}()

	// wait for mac proxy to start. server will always be running
	pollTicker := time.NewTicker(serverPollInterval)
	timeout := time.NewTimer(serverStartTimeout)
loop:
	for {
		select {
		case <-pollTicker.C:
			// check if server is running
			conn, err := netx.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				continue
			}

			// connected = fwd running
			conn.Close()
			err = nil
			break loop
		case destErr := <-destErrC:
			// server failed to start
			err = fmt.Errorf("start server: %w", destErr)
			break loop
		case <-timeout.C:
			// timeout
			err = fmt.Errorf("start server: timeout")
			break loop
		}
	}
	pollTicker.Stop()
	timeout.Stop()
	if err != nil {
		return err
	}

	m.mu.Lock()
	srcAgentCid := m.srcAgentCid
	m.mu.Unlock()

	execReq := &dockertypes.ContainerExecCreateRequest{
		Cmd:          cmdBuilder(port),
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   srcCwd,
	}

	// we're trying to get a direct connection with minimal copying
	// xattrs needed to preserve overlayfs opaque dirs
	_, err = execAs(srcClient, srcAgentCid, execReq)
	if err != nil {
		// if it failed, then we may not have connected to the TCP server, so dest will hang.
		// make an attempt to unfreeze it
		conn, err2 := netx.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err2 == nil {
			conn.Close()
		}

		return fmt.Errorf("exec src sync: %w", err)
	}

	// wait for dest to finish
	err = <-destErrC
	if err != nil {
		return fmt.Errorf("wait dest sync: %w", err)
	}

	return nil
}
