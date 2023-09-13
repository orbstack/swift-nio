package hcli

import (
	"net"
	"sync"

	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

var Client = sync.OnceValue(func() *hclient.Client {
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
