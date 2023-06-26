package hcli

import (
	"net"

	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/syncx"
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
