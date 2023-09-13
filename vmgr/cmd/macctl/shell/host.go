package shell

import (
	"errors"
	"net"
	"os"
	"sync"

	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

var HostUser = sync.OnceValue(func() string {
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
