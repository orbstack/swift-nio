package main

import (
	"os"
	"path"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
)

func main() {
	cmd := path.Base(os.Args[0])
	var err error
	switch cmd {
	// control-only command mode
	case "mvctl":
		fallthrough
	case "linuxctl":
		err = runCtl(false)
	// control or shell, depending on args
	case "mv":
		fallthrough
	case "linux":
		err = runCtl(true)
	// command stub mode
	default:
		err = runCommandStub()
	}

	if err != nil {
		panic(err)
	}
}

func translatePath(p string) string {
	// canonicalize first
	p = path.Clean(p)

	// common case: is it linked?
	for _, linkPrefix := range mounts.LinkedPaths {
		if p == linkPrefix || strings.HasPrefix(p, linkPrefix+"/") {
			return p
		}
	}

	// nope, needs translation
	return mounts.VirtiofsMountpoint + p
}

func runCommandStub() error {
}

func runCtl(fallbackToShell bool) error {
	return nil
}
