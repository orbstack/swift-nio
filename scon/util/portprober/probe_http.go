package portprober

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

func probePortHTTP(ctx context.Context, dialer *net.Dialer, host string, port uint16, _ string) (bool, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
		},
	}

	// favicon.ico is less likely to trigger any weird behavior?
	req, err := http.NewRequestWithContext(ctx, "HEAD", fmt.Sprintf("http://%v:%v/favicon.ico", host, port), nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("User-Agent", "OrbStack-Server-Detection/1.0 (https://orbsta.cc/srvdetect)")

	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()

	return true, nil
}
