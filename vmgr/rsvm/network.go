package rsvm

/*
#include <stdlib.h>
#include <stdint.h>
#include <sys/uio.h>

// iovs = struct iovec *
// (obfuscated for cgocheck)
int rsvm_network_write_packet(uintptr_t handle, uintptr_t iovs, size_t num_iovs, size_t total_len);
*/
import "C"
import (
	"runtime"
	"runtime/cgo"
	"unsafe"

	"github.com/orbstack/macvirt/vmgr/vnet/cblink"
	"golang.org/x/sys/unix"

	_ "go4.org/unsafe/assume-no-moving-gc"
)

const HandleGvisor uintptr = 0

type NetCallbacks struct {
	handle uintptr
}

func NewNetCallbacks(handle uintptr) *NetCallbacks {
	return &NetCallbacks{
		handle: handle,
	}
}

func (cb *NetCallbacks) WritePacket(iovecs []unix.Iovec, totalLen int) int32 {
	/*
	 * cgocheck complains about the iovecs buffers not being pinned.
	 * However, adding every iov to a Pinner has a non-negligible cost, especially
	 * because we can't reuse Pinners easily (this can be called concurrently).
	 *
	 * Even the stdlib's net.Buffers/writeBuffers uses for iovecs, so it's safe in practice.
	 * This KeepAlive is enough, and we import go4.org/unsafe/assume-no-moving-gc to make sure
	 * it complains loudly if Go ever gets a moving GC and breaks this.
	 */
	defer runtime.KeepAlive(iovecs)

	iovsPtr := unsafe.SliceData(iovecs)
	// uintptr dance to hide the pointer from cgocheck
	return int32(C.rsvm_network_write_packet(C.uintptr_t(cb.handle), C.uintptr_t(uintptr(unsafe.Pointer(iovsPtr))), C.size_t(len(iovecs)), C.size_t(totalLen)))
}

//export rsvm_go_gvisor_network_write_packet
func rsvm_go_gvisor_network_write_packet(handle uintptr, iovs *C.struct_iovec, numIovs C.size_t, totalLen C.size_t) int32 {
	ep := cgo.Handle(handle).Value().(*cblink.CallbackEndpoint)

	iovecs := unsafe.Slice((*unix.Iovec)(unsafe.Pointer(iovs)), numIovs)
	ret := ep.InjectInbound(iovecs, int(totalLen))
	if ret != 0 {
		return -int32(ret)
	}

	return 0
}
