package hcli

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/scon/hclient"
)

var (
	cachedClient *hclient.Client
)

func Client() *hclient.Client {
	if cachedClient == nil {
		conn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			panic(err)
		}
		client, err := hclient.New(conn)
		if err != nil {
			panic(err)
		}
		cachedClient = client
	}

	return cachedClient
}
