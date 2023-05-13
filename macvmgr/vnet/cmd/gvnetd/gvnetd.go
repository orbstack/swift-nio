package main

import (
	"os"
	"runtime"

	"github.com/orbstack/macvirt/macvmgr/vnet"
	"github.com/sirupsen/logrus"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "01-02 15:04:05",
	})

	opts := vnet.NetOptions{
		LinkMTU: vnet.PreferredMTU,
	}

	_, err := vnet.StartQemuFd(opts, os.Stdin)
	check(err)

	// block forever
	runtime.Goexit()
}
