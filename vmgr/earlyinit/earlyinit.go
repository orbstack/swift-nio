package earlyinit

import (
	"runtime"

	"github.com/orbstack/macvirt/vmgr/conf"
)

const AllowProdHeapProfile = false

func init() {
	if !conf.Debug() {
		if AllowProdHeapProfile {
			runtime.MemProfileRate = 1
		} else {
			runtime.MemProfileRate = 0
		}
	} else {
		// for testing prod heap profile in debug
		if AllowProdHeapProfile {
			runtime.MemProfileRate = 1
		}
	}
}
