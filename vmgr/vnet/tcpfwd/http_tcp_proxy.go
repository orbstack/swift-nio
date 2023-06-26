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

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/orbstack/macvirt/vmgr/vnet/proxy"
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

func newHTTPProxy(u *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	if forward != nil {
		return nil, errors.New("http proxy does not support chaining")
	}

	var proxy httpProxy
	if u.Scheme == "https" {
		proxy.isTls = true
	}

	host := u.Host
	if u.Port() == "" {
		if proxy.isTls {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}
	proxy.host = host

	if u.User != nil {
		proxy.authHeader = "Basic " + basicAuth(u.User)
	}

	proxy.userAgent = appid.UserAppName + "/" + appver.Get().Short

	return &proxy, nil
}

func basicAuth(user *url.Userinfo) string {
	password, _ := user.Password()

	// in http basic auth we always have :, even if password is empty
	auth := user.Username() + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (p *httpProxy) DialContext(ctx context.Context, network, addr string) (conn net.Conn, err error) {
	var dialer net.Dialer
	conn, err = dialer.DialContext(ctx, "tcp", p.host)
	if err != nil {
		return nil, err
	}
	// save tcp conn even if we wrap it with tls later
	tcpConn := conn.(*net.TCPConn)

	defer func() {
		if err != nil && conn != nil {
			conn.Close()
		}
	}()

	if p.isTls {
		// dial tcp and wrap it ourselves in order to set/unset TCP_NODELAY
		// (tls.Conn does not expose the underlying net.Conn)

		// find tls host
		tlsHost, _, err := net.SplitHostPort(p.host)
		if err != nil {
			return conn, err
		}

		// wrap and handshake
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: tlsHost,
		})
		err = tlsConn.HandshakeContext(ctx)
		if err != nil {
			return conn, err
		}

		// swap conn
		conn = tlsConn
	}

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

	// set nodelay early for tls, or we'll lose the tcp conn reference
	// other port doesn't matter, only service does (client port should be ephemeral)
	// we set this *after* CONNECT because CONNECT benefits from the default TCP_NODELAY
	err = setExtNodelay(tcpConn, 0)
	if err != nil {
		return
	}

	return conn, nil
}

func (p *httpProxy) Dial(network, addr string) (net.Conn, error) {
	return p.DialContext(context.Background(), network, addr)
}
