package scli

import (
	"github.com/orbstack/macvirt/macvmgr/syncx"
	"github.com/orbstack/macvirt/macvmgr/vmclient"
	"github.com/orbstack/macvirt/scon/sclient"
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
