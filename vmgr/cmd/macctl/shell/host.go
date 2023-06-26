package shell

import (
	"errors"
	"net"
	"os"

	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/syncx"
)

var (
	onceHostUser syncx.Once[string]
)

func HostUser() string {
	return onceHostUser.Do(func() string {
		conn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// in isolated machine or docker - fall back to $USER
				return ""
			}

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
