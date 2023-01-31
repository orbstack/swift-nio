package scli

import (
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

	url := Conf().RpcURL

	if Conf().ControlVM {
		//TODO start vm
	}

	client, err := sclient.New(url)
	check(err)

	cachedClient = client
	return client
}
