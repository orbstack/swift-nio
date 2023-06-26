package sshenv

import (
	"net"
	"net/url"
)

func p(s string) *string {
	return &s
}

type ProxyTranslatorFunc func(host, port string) *string

func ProxyToMac(host, port string) *string {
	if host == "host.orb.internal" || host == "host.docker.internal" || host == "host.internal" || host == "host.lima.internal" || host == "host" {
		if port == "" {
			return p("localhost")
		} else {
			// this is hostname so we don't need net.JoinHostPort
			return p("localhost:" + port)
		}
	}

	return nil
}

func ProxyToLinux(host, port string) *string {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		if port == "" {
			return p("host.orb.internal")
		} else {
			// this is hostname so we don't need net.JoinHostPort
			return p("host.orb.internal:" + port)
		}
	}

	return nil
}

func translateOneProxyUrl(value string, transFn ProxyTranslatorFunc) (string, error) {
	// parse url
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}

	// split host:port
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		if addrError, ok := err.(*net.AddrError); ok && addrError.Err == "missing port in address" {
			// no port, use default
			host = u.Host
		} else {
			return "", err
		}
	}

	// translate
	if newHost := transFn(host, port); newHost != nil {
		u.Host = *newHost
		return u.String(), nil
	}

	return value, nil
}
