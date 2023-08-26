package earlyinit

import (
	"runtime"

	"github.com/orbstack/macvirt/vmgr/conf"
)

func init() {
	if !conf.Debug() {
		runtime.MemProfileRate = 0
	}
}
