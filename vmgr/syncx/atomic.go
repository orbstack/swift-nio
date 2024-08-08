package syncx

import "sync/atomic"

func AtomicOrUint32(addr *uint32, val uint32) {
	for {
		old := atomic.LoadUint32(addr)
		if atomic.CompareAndSwapUint32(addr, old, old|val) {
			return
		}
	}
}
