package main

import (
	"bufio"
	"context"
	"log"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"
	"golang.org/x/sys/unix"
)

const (
	gatewayIP    = "192.168.127.1"
	sshHostPort  = "192.168.127.2:22"
	dgramSockBuf = 256 * 1024
	gvproxyMtu   = 65520
)

func startGvproxyPair() (file *os.File, err error) {
	config := types.Configuration{
		Debug:             false,
		MTU:               gvproxyMtu,
		Subnet:            "192.168.127.0/24",
		GatewayIP:         gatewayIP,
		GatewayMacAddress: "5a:94:ef:e4:0c:dd",
		DHCPStaticLeases: map[string]string{
			"192.168.127.2": "5a:94:ef:e4:0c:ee",
		},
		DNS: []types.Zone{
			{
				Name: "containers.internal.",
				Records: []types.Record{
					{
						Name: "gateway",
						IP:   net.ParseIP(gatewayIP),
					},
					{
						Name: "host",
						IP:   net.ParseIP("192.168.127.254"),
					},
				},
			},
			{
				Name: "crc.testing.", // still used by current version of podman machine CNI
				Records: []types.Record{
					{
						Name: "gateway",
						IP:   net.ParseIP(gatewayIP),
					},
					{
						Name: "host",
						IP:   net.ParseIP("192.168.127.254"),
					},
				},
			},
		},
		DNSSearchDomains: searchDomains(),
		Forwards:         map[string]string{},
		NAT: map[string]string{
			"192.168.127.254": "127.0.0.1",
		},
		GatewayVirtualIPs: []string{"192.168.127.254"},
		Protocol:          types.BessProtocol,
	}

	return runGvproxy(&config)
}

func runGvproxy(config *types.Configuration) (file0 *os.File, err error) {
	vn, err := virtualnetwork.New(config)
	if err != nil {
		return
	}

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF, dgramSockBuf)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[0], unix.SOL_SOCKET, unix.SO_RCVBUF, dgramSockBuf*4)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[1], unix.SOL_SOCKET, unix.SO_SNDBUF, dgramSockBuf)
	if err != nil {
		return
	}
	err = unix.SetsockoptUint64(fds[1], unix.SOL_SOCKET, unix.SO_RCVBUF, dgramSockBuf*4)
	if err != nil {
		return
	}
	// fd 0 for VMM, fd 1 for us
	err = unix.SetNonblock(fds[0], true)
	if err != nil {
		return
	}
	file0 = os.NewFile(uintptr(fds[0]), "socketpair0")
	file1 := os.NewFile(uintptr(fds[1]), "socketpair1")
	conn1, err := net.FileConn(file1)
	if err != nil {
		return
	}

	ctx := context.Background()
	go func() {
		err := vn.AcceptBess(ctx, conn1)
		if err != nil {
			// TODO error handling
			panic(err)
		}
	}()

	return
}

func searchDomains() []string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		f, err := os.Open("/etc/resolv.conf")
		if err != nil {
			log.Printf("open file error: %v", err)
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		searchPrefix := "search "
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), searchPrefix) {
				searchDomains := strings.Split(strings.TrimPrefix(sc.Text(), searchPrefix), " ")
				log.Printf("Using search domains: %v", searchDomains)
				return searchDomains
			}
		}
		if err := sc.Err(); err != nil {
			log.Printf("scan file error: %v", err)
			return nil
		}
	}
	return nil
}
