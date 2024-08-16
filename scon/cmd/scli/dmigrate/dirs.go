package dmigrate

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/alessio/shellescape"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

const (
	serverPollInterval = 25 * time.Millisecond
	serverStartTimeout = 10 * time.Second
)

func findFreeTCPPort() (int, error) {
	// zero-port listener
	listener, err := netx.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	// get port
	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

func (m *Migrator) startSyncServer() error {
	syncPort, err := findFreeTCPPort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	m.syncPort = syncPort

	// start server
	err = scli.Client().InternalDockerMigrationRunSyncServer(types.InternalDockerMigrationRunSyncServerRequest{
		Port: syncPort,
	})
	if err != nil {
		return fmt.Errorf("start sync server: %w", err)
	}

	// wait for mac proxy to start. server will always be running
	pollTicker := time.NewTicker(serverPollInterval)
	timeout := time.NewTimer(serverStartTimeout)
loop:
	for {
		select {
		case <-pollTicker.C:
			// check if server is running
			conn, err := netx.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", syncPort))
			if err != nil {
				continue
			}

			// connected = fwd running
			conn.Close()
			err = nil
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

	return nil
}

func (m *Migrator) syncDirs(srcClient *dockerclient.Client, srcs []string, destClient *dockerclient.Client, dest string) error {
	if len(srcs) != 1 {
		return fmt.Errorf("must have exactly 1 src dir")
	}

	dirSyncReq := types.InternalDockerMigrationSyncDirsRequest{
		JobID: rand.Uint64(),
		Dirs:  []string{dest},
	}
	dirSyncReqBytes, err := json.Marshal(&dirSyncReq)
	if err != nil {
		return fmt.Errorf("marshal dir sync req: %w", err)
	}

	cmdBuilder := func(port int) []string {
		return []string{
			"bash",
			"-c",
			// /dev/tcp is raw socket fd
			fmt.Sprintf("set -e; ( echo %s; tar --numeric-owner -cf - . ) > /dev/tcp/host.docker.internal/%d", shellescape.Quote(string(dirSyncReqBytes)), port),
		}
	}

	return m.syncDirsGeneric(srcClient, cmdBuilder, srcs[0], destClient, &dirSyncReq)
}

func (m *Migrator) syncDirsGeneric(srcClient *dockerclient.Client, cmdBuilder func(int) []string, srcCwd string, destClient *dockerclient.Client, dirSyncReq *types.InternalDockerMigrationSyncDirsRequest) error {
	execReq := &dockertypes.ContainerExecCreateRequest{
		Cmd:          cmdBuilder(m.syncPort),
		AttachStdout: true,
		AttachStderr: true,
		WorkingDir:   srcCwd,
	}

	// we're trying to get a direct connection with minimal copying
	_, err := srcClient.Exec(m.srcAgentCid, execReq)
	if err != nil {
		// if it failed, then we may not have connected to the TCP server, so dest will hang.
		// make an attempt to unfreeze it
		conn, err2 := netx.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", m.syncPort))
		if err2 == nil {
			conn.Close()
		}

		return fmt.Errorf("exec src sync: %w", err)
	}

	// wait
	err = scli.Client().InternalDockerMigrationWaitSync(types.InternalDockerMigrationWaitSyncRequest{
		JobID: dirSyncReq.JobID,
	})
	if err != nil {
		return fmt.Errorf("wait sync: %w", err)
	}

	return nil
}
