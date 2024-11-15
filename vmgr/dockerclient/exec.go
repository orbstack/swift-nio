package dockerclient

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func DemuxOutput(r io.Reader, w io.Writer) error {
	// decode multiplexed
	for {
		hdr := make([]byte, 8)
		_, err := r.Read(hdr)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			} else {
				return fmt.Errorf("read header: %w", err)
			}
		}
		// big endian uint32 from last 4 bytes
		size := binary.BigEndian.Uint32(hdr[4:8])
		// read that amount
		buf := make([]byte, size)
		n := 0
		for n < int(size) {
			nr, err := r.Read(buf[n:])
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				} else {
					return fmt.Errorf("read body: %w", err)
				}
			}
			n += nr
		}
		// write out
		w.Write(buf)
	}
}

func (c *Client) ExecCreate(cid string, execReq *dockertypes.ContainerExecCreateRequest) (*dockertypes.ContainerExecCreateResponse, error) {
	var execResp dockertypes.ContainerExecCreateResponse
	if err := c.Call("POST", "/containers/"+url.PathEscape(cid)+"/exec", execReq, &execResp); err != nil {
		return nil, err
	}
	return &execResp, nil
}

func (c *Client) ExecInspect(execID string) (*dockertypes.ContainerExecInspect, error) {
	var inspectResp dockertypes.ContainerExecInspect
	if err := c.Call("POST", "/exec/"+execID+"/json", nil, &inspectResp); err != nil {
		return nil, err
	}
	return &inspectResp, nil
}

func (c *Client) Exec(cid string, execReq *dockertypes.ContainerExecCreateRequest) (string, error) {
	execCreate, err := c.ExecCreate(cid, execReq)
	if err != nil {
		return "", fmt.Errorf("create exec: %w", err)
	}

	// run the tar
	reader, err := c.StreamRead("POST", "/exec/"+url.PathEscape(execCreate.ID)+"/start", dockertypes.ContainerExecStartRequest{
		Detach: false,
	})
	if err != nil {
		return "", fmt.Errorf("start exec: %w", err)
	}
	defer reader.Close()

	var output bytes.Buffer
	err = DemuxOutput(reader, &output)
	if err != nil {
		return "", fmt.Errorf("demux output: %w", err)
	}

	// check exec exit status
	execInspect, err := c.ExecInspect(execCreate.ID)
	if err != nil {
		return "", fmt.Errorf("inspect exec: %w", err)
	}

	if execInspect.ExitCode != 0 {
		return "", fmt.Errorf("exec exit code: %d; output: %s", execInspect.ExitCode, output.String())
	}

	// success
	return output.String(), nil
}

func (c *Client) ExecStream(cid string, execReq *dockertypes.ContainerExecCreateRequest) (net.Conn, error) {
	execCreate, err := c.ExecCreate(cid, execReq)
	if err != nil {
		return nil, fmt.Errorf("create exec: %w", err)
	}

	// upgrade to tcp
	conn, err := c.streamHijack("POST", "/exec/"+execCreate.ID+"/start", dockertypes.ContainerExecStartRequest{
		Detach: false,
		Tty:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("start exec: %w", err)
	}

	return conn, nil
}
