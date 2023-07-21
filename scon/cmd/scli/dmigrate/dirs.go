package dmigrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
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
	_, err := execAs(srcClient, m.srcAgentCid, execReq)
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
