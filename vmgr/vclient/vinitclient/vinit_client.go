package vinitclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	baseURL = "http://vcontrol"

	// very liberal to avoid false positive
	RequestTimeout = 1 * time.Minute
)

type DialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type VinitClient struct {
	http *http.Client
}

func NewVinitClient(dialContext DialContextFunc) *VinitClient {
	return &VinitClient{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext:  dialContext,
				MaxIdleConns: 3,
			},
			Timeout: RequestTimeout,
		},
	}
}

func (vc *VinitClient) Get(endpoint string) (*http.Response, error) {
	req, err := http.NewRequest("GET", baseURL+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := vc.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}

func (vc *VinitClient) Post(endpoint string, body any, out any) error {
	msg, err := json.Marshal(body)
	if err != nil {
		return err
	}

	reader := bytes.NewReader(msg)
	req, err := http.NewRequest("POST", baseURL+"/"+endpoint, reader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := vc.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %w", err)
		}
	} else {
		io.Copy(io.Discard, resp.Body)
	}

	return nil
}

func (vc *VinitClient) Close() error {
	vc.http.CloseIdleConnections()
	return nil
}

func readError(resp *http.Response) error {
	// read error message
	errBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error body: %s (%s)", err, resp.Status)
	}

	return fmt.Errorf("[vc] %s (%s)", string(errBody), resp.Status)
}
