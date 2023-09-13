package earlyinit

import (
	"runtime"

	"github.com/orbstack/macvirt/scon/conf"
)

func init() {
	// we always import pprof, but don't use it in release
	// so disable memprofile overhead
	if !conf.Debug() {
		runtime.MemProfileRate = 0
	}
}
