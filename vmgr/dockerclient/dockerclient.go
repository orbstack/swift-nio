package dockerclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client struct {
	http *http.Client
}

func New(httpC *http.Client) *Client {
	return &Client{
		http: httpC,
	}
}

func (c *Client) Call(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body: %s", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, "http://docker"+path, reader)
	if err != nil {
		return fmt.Errorf("create request: %s", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 304 { // Not Modified
			return nil
		}

		// read error message
		var jsonError struct {
			Message string `json:"message"`
		}
		err = json.NewDecoder(resp.Body).Decode(&jsonError)
		if err != nil {
			return fmt.Errorf("decode error: %s (%s)", err, resp.Status)
		}

		return fmt.Errorf("[Docker] %s", jsonError.Message)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %s", err)
		}
	}

	return nil
}

func (c *Client) Stream(method, path string) (io.ReadCloser, error) {
	req, err := http.NewRequest(method, "http://docker"+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %s", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %s", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp.Body, nil
}
