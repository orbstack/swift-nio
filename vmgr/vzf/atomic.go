package vzf

import (
	"sync/atomic"
	"unsafe"
)

type atomicUnsafePointer struct {
	p unsafe.Pointer
}

func (p *atomicUnsafePointer) Load() unsafe.Pointer {
	return atomic.LoadPointer(&p.p)
}

func (p *atomicUnsafePointer) Store(v unsafe.Pointer) {
	atomic.StorePointer(&p.p, v)
}

func (p *atomicUnsafePointer) Swap(v unsafe.Pointer) unsafe.Pointer {
	return atomic.SwapPointer(&p.p, v)
}

func (p *atomicUnsafePointer) CompareAndSwap(old, new unsafe.Pointer) bool {
	return atomic.CompareAndSwapPointer(&p.p, old, new)
}
