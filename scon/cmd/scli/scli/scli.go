package scli

import (
	"sync"

	"github.com/orbstack/macvirt/scon/sclient"
	"github.com/orbstack/macvirt/vmgr/vmclient"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

var Client = sync.OnceValue(func() *sclient.SconClient {
	if Conf().ControlVM {
		err := vmclient.EnsureSconVM()
		check(err)
	}

	client, err := sclient.New(Conf().RpcNetwork, Conf().RpcAddr)
	check(err)

	return client
})
