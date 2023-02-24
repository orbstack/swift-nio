package shell

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/syncx"
	"github.com/kdrag0n/macvirt/scon/hclient"
)

var (
	onceHostUser syncx.Once[string]
)

func HostUser() string {
	return onceHostUser.Do(func() string {
		conn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			panic(err)
		}
		defer conn.Close()

		client, err := hclient.New(conn)
		if err != nil {
			panic(err)
		}
		defer client.Close()

		u, err := client.GetUser()
		if err != nil {
			panic(err)
		}

		return u.Username
	})
}
