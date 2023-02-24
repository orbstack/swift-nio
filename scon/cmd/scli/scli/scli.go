package scli

import (
	"github.com/kdrag0n/macvirt/macvmgr/syncx"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/sclient"
)

var (
	onceClient syncx.Once[*sclient.SconClient]
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func Client() *sclient.SconClient {
	return onceClient.Do(func() *sclient.SconClient {
		if Conf().ControlVM {
			err := vmclient.EnsureSconVM()
			check(err)
		}

		client, err := sclient.New(Conf().RpcNetwork, Conf().RpcAddr)
		check(err)

		return client
	})
}
