package main

import (
	"encoding/base64"
	"os"

	"github.com/lxc/go-lxc"
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

func runForkStart() {
	paramsData, err := base64.StdEncoding.DecodeString(os.Args[2])
	check(err)
	var params LxcForkParams
	err = gobDecode(paramsData, &params)
	check(err)

	lc, err := lxc.NewContainer(params.ID, params.LxcDir)
	check(err)
	err = lc.SetLogFile(params.LogFile)
	check(err)
	lc.SetVerbosity(params.Verbosity)
	lc.SetLogLevel(params.LogLevel)
	for _, config := range params.Configs {
		err = lc.SetConfigItem(config.Key, config.Value)
		check(err)
	}

	err = lc.Start()
	check(err)
}
