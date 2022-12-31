package main

import (
	"github.com/mdlayher/vsock"
	"github.com/songgao/water"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	config := water.Config{
		DeviceType: water.TAP,
	}
	ifce, err := water.New(config)
	check(err)
	ifce.

	vsock.Addr
}
