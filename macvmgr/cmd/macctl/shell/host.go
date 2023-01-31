package shell

import (
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/scon/hclient"
)

var (
	cachedHostUser *string
)

func HostUser() string {
	if cachedHostUser != nil {
		return *cachedHostUser
	}

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

	cachedHostUser = &u.Username
	return u.Username
}
