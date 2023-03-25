package main

import (
	"encoding/base64"
	"os"

	"github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"
)

type KeyValue[T comparable] struct {
	Key   T
	Value T
}

type LxcForkParams struct {
	ID        string
	LxcDir    string
	LogFile   string
	Verbosity lxc.Verbosity
	LogLevel  lxc.LogLevel
	Configs   []KeyValue[string]
}

func checkFork(err error) {
	if err != nil {
		// avoid bringing in fmt
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
}

func runForkStart() {
	// first thing: prctl(PR_SET_PDEATHSIG) so we exit when parent exits
	// XXX: this would break if Go OS threads exit, but they don't
	// so we can rely on parent thread staying around
	err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0)
	checkFork(err)

	paramsData, err := base64.StdEncoding.DecodeString(os.Args[2])
	checkFork(err)
	var params LxcForkParams
	err = gobDecode(paramsData, &params)
	checkFork(err)

	lc, err := lxc.NewContainer(params.ID, params.LxcDir)
	checkFork(err)
	err = lc.SetLogFile(params.LogFile)
	checkFork(err)
	lc.SetVerbosity(params.Verbosity)
	lc.SetLogLevel(params.LogLevel)
	for _, config := range params.Configs {
		err = lc.SetConfigItem(config.Key, config.Value)
		checkFork(err)
	}

	err = lc.Start()
	checkFork(err)
}
