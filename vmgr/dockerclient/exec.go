package dockerclient

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

func demuxOutput(r io.Reader, w io.Writer) error {
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

func (c *Client) Exec(cid string, execReq *dockertypes.ContainerExecCreateRequest) (string, error) {
	var execResp dockertypes.ContainerExecCreateResponse
	err := c.Call("POST", "/containers/"+cid+"/exec", execReq, &execResp)
	if err != nil {
		return "", fmt.Errorf("create exec: %w", err)
	}

	// run the tar
	reader, err := c.StreamRead("POST", "/exec/"+execResp.ID+"/start", dockertypes.ContainerExecStartRequest{
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
	err = c.Call("GET", "/exec/"+execResp.ID+"/json", nil, &execInspect)
	if err != nil {
		return "", fmt.Errorf("inspect exec: %w", err)
	}

	if execInspect.ExitCode != 0 {
		return "", fmt.Errorf("exec exit code: %d; output: %s", execInspect.ExitCode, output.String())
	}

	// success
	return output.String(), nil
}
