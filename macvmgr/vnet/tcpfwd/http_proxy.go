package tcpfwd

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appver"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/proxy"
)

func init() {
	proxy.RegisterDialerType("http", newHTTPProxy)
	proxy.RegisterDialerType("https", newHTTPProxy)
}

type httpProxy struct {
	isTls      bool
	host       string
	authHeader string
	userAgent  string
}

func newHTTPProxy(uri *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	if forward != nil {
		return nil, errors.New("http proxy does not support chaining")
	}

	var proxy httpProxy
	if uri.Scheme == "https" {
		proxy.isTls = true
	}

	host := uri.Host
	if uri.Port() == "" {
		if proxy.isTls {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	proxy.host = host

	if uri.User != nil {
		pass, _ := uri.User.Password()
		proxy.authHeader = "Basic " + basicAuth(uri.User.Username(), pass)
	}

	proxy.userAgent = appid.UserAppName + "/" + appver.Get().Short

	return &proxy, nil
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (p *httpProxy) DialContext(ctx context.Context, network, addr string) (conn net.Conn, err error) {
	if p.isTls {
		var tlsDialer tls.Dialer
		conn, err = tlsDialer.DialContext(ctx, "tcp", p.host)
	} else {
		var dialer net.Dialer
		conn, err = dialer.DialContext(ctx, "tcp", p.host)
	}
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil {
			conn.Close()
		}
	}()

	httpUrl, err := url.Parse("http://" + addr)
	if err != nil {
		return
	}
	httpUrl.Scheme = ""

	req, err := http.NewRequest("CONNECT", httpUrl.String(), nil)
	if err != nil {
		return
	}
	req.Close = false
	if p.authHeader != "" {
		req.Header.Set("Proxy-Authorization", p.authHeader)
	}
	req.Header.Set("User-Agent", p.userAgent)

	err = req.Write(conn)
	if err != nil {
		err = fmt.Errorf("write CONNECT: %w", err)
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		err = fmt.Errorf("read CONNECT: %w", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		err = fmt.Errorf("proxy CONNECT: %s", resp.Status)
		return
	}

	return conn, nil
}

func (p *httpProxy) Dial(network, addr string) (net.Conn, error) {
	return p.DialContext(context.Background(), network, addr)
}
