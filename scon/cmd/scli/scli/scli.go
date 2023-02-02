package scli

import (
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/sclient"
)

var (
	cachedClient *sclient.SconClient
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func Client() *sclient.SconClient {
	if cachedClient != nil {
		return cachedClient
	}

	if Conf().ControlVM {
		err := vmclient.EnsureSconVM()
		check(err)
	}

	client, err := sclient.New(Conf().RpcNetwork, Conf().RpcAddr)
	check(err)

	cachedClient = client
	return client
}
