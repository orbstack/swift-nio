package hcli

import (
	"github.com/orbstack/macvirt/vmgr/util/errorx"
	"net"
	"sync"

	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

var Client = sync.OnceValue(func() *hclient.Client {
	conn, err := net.Dial("unix", mounts.HcontrolSocket)
	errorx.CheckCLI(err)

	client, err := hclient.New(conn)
	errorx.CheckCLI(err)

	return client
})
