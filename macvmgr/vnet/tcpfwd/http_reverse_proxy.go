package tcpfwd

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/kdrag0n/macvirt/macvmgr/vnet/proxy"
	"github.com/sirupsen/logrus"
)

type httpReverseProxy struct {
	proxy        *httputil.ReverseProxy
	server       *http.Server
	loopListener loopListener
}

func (h *httpReverseProxy) HandleConn(conn net.Conn) {
	h.loopListener.ch <- conn
}

type loopListener struct {
	ch chan net.Conn
}

func newLoopListener() *loopListener {
	return &loopListener{
		ch: make(chan net.Conn),
	}
}

func (l *loopListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}

	return conn, nil
}

func (l *loopListener) Addr() net.Addr {
	return &net.TCPAddr{
		IP:   net.IPv4zero,
		Port: 0,
	}
}

func (l *loopListener) Close() error {
	close(l.ch)
	return nil
}

func (h *httpReverseProxy) Close() error {
	return h.server.Close()
}

func newHttpReverseProxy(proxyUrl *url.URL, perHostFilter *proxy.PerHost) *httpReverseProxy {
	// do we need auth?
	authHeader := ""
	if proxyUrl.User != nil {
		authHeader = "Basic " + basicAuth(proxyUrl.User)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// prefer Host header if we have it
			// this way we pass the domain name to the proxy
			host := r.In.Host
			if host == "" {
				// get it from our gvisor side of the request (IP) if Host header is not set
				localAddr := r.In.Context().Value(http.LocalAddrContextKey)
				if localAddr != nil {
					host = localAddr.(net.Addr).String()
				}
			}

			// this is our only chance to test per-host against a real domain name
			if perHostFilter != nil && perHostFilter.TestBypass(host) {
				logrus.Debugf("bypassing proxy for %s (http)", host)
				// bypass proxy
				r.SetURL(&url.URL{
					Scheme: "http",
					Host:   host,
					Path:   r.In.URL.Path,
				})
			} else {
				// use proxy
				r.SetURL(&url.URL{
					Scheme: proxyUrl.Scheme,
					Host:   proxyUrl.Host,
					Path:   "http://" + host,
				})
			}

			r.Out.Host = r.In.Host

			// set Proxy-Authorization header if we need it
			if authHeader != "" {
				r.Out.Header.Set("Proxy-Authorization", authHeader)
			}
		},
	}

	server := &http.Server{
		Handler: proxy,
	}

	listener := newLoopListener()
	go server.Serve(listener)

	return &httpReverseProxy{
		proxy:        proxy,
		server:       server,
		loopListener: *listener,
	}
}
