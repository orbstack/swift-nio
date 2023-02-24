package hcli

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/syncx"
	"github.com/kdrag0n/macvirt/scon/hclient"
)

var (
	onceClient syncx.Once[*hclient.Client]
)

func Client() *hclient.Client {
	return onceClient.Do(func() *hclient.Client {
		conn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			panic(err)
		}
		client, err := hclient.New(conn)
		if err != nil {
			panic(err)
		}
		return client
	})
}
