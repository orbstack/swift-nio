package portprober

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

func probePortHTTP(ctx context.Context, dialer *net.Dialer, host string, port uint16, _ string) (bool, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
		},

		// don't follow redirects
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// favicon.ico is less likely to trigger any weird behavior?
	hostString := net.JoinHostPort(host, strconv.Itoa(int(port)))
	req, err := http.NewRequestWithContext(ctx, "HEAD", fmt.Sprintf("http://%s/favicon.ico", hostString), nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("User-Agent", "OrbStack-Server-Detection/1.0 (https://orbsta.cc/srvdetect)")

	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	resp.Body.Close()

	return true, nil
}
