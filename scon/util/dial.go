package util

import (
	"fmt"
	"net"
	"time"
)

const (
	retryDialInterval = 100 * time.Millisecond
	retryDialTimeout  = 5 * time.Second
)

func RetryDial(network, addr string) (net.Conn, error) {
	var conn net.Conn
	var err error

	start := time.Now()
	for time.Since(start) < retryDialTimeout {
		conn, err = net.Dial(network, addr)
		if err == nil {
			return conn, nil
		}

		time.Sleep(retryDialInterval)
	}

	return nil, fmt.Errorf("retry dial timeout: %w", err)
}
