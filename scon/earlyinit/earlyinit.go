package earlyinit

import (
	"runtime"

	"github.com/orbstack/macvirt/scon/conf"
)

func init() {
	if !conf.Debug() {
		runtime.MemProfileRate = 0
	}
}
