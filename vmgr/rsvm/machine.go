package rsvm

/*
#cgo LDFLAGS: -framework Hypervisor

#include <stdlib.h>
#include <stdint.h>

struct GResultCreate {
    void* ptr;
    char* err;
};

struct GResultErr {
    char* err;
};

struct GResultIntErr {
    int64_t value;
    char* err;
};

struct GResultCreate rsvm_new_machine(uintptr_t handle, const char* config_json_str);
struct GResultErr rsvm_machine_start(void* ptr);
struct GResultErr rsvm_machine_stop(void* ptr);
void rsvm_machine_destroy(void* ptr);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/orbstack/macvirt/vmgr/vmm"
)

func errFromC(err *C.char) error {
	if err == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(err))
	return errors.New(C.GoString(err))
}

func errFromResult(result C.struct_GResultErr) error {
	return errFromC(result.err)
}

// not possible to have more than one VM in the same process due to HVF limitations, so no point in supporting that
var vmCreated atomic.Bool

// ... which means state chan can be global
var stateChan = make(chan vmm.MachineState, 1)

//export rsvm_go_on_state_change
func rsvm_go_on_state_change(state int) {
	// no lock needed: channel never changes
	ch := stateChan
	go func() {
		ch <- vmm.MachineState(state)
	}()
}

type machine struct {
	mu     sync.RWMutex
	ptr    atomicUnsafePointer
	handle cgo.Handle

	retainFiles []*os.File
}

func (m monitor) NewMachine(spec *vmm.VzSpec, retainFiles []*os.File) (vmm.Machine, error) {
	if !vmCreated.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("only one VM can be created in a process")
	}

	// encode to json
	specStr, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}

	// create Go object
	vm := &machine{
		retainFiles: retainFiles,
	}
	handle := cgo.NewHandle(vm)
	vm.handle = handle

	// call cgo
	cstr := C.CString(string(specStr))
	defer C.free(unsafe.Pointer(cstr))
	result := C.rsvm_new_machine(C.uintptr_t(handle), cstr)

	// wait for result
	if result.err != nil {
		handle.Delete()
		return nil, errFromC(result.err)
	}

	// set ptr
	vm.ptr.Store(result.ptr)
	// ref ok: this just drops Go ref; Swift ref is still held if alive
	runtime.SetFinalizer(vm, (*machine).Close)

	return vm, nil
}

func (m *machine) callGenericErr(fn func(unsafe.Pointer) C.struct_GResultErr) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ptr := m.ptr.Load()
	if ptr == nil {
		return errors.New("machine closed")
	}

	res := fn(ptr)
	return errFromC(res.err)
}

func (m *machine) callGenericErrInt(fn func(unsafe.Pointer) C.struct_GResultIntErr) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ptr := m.ptr.Load()
	if ptr == nil {
		return 0, errors.New("machine closed")
	}

	res := fn(ptr)
	return int64(res.value), errFromC(res.err)
}

func (m *machine) Start() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.rsvm_machine_start(ptr)
	})
}

func (m *machine) ForceStop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.rsvm_machine_stop(ptr)
	})
}

func (m *machine) RequestStop() error {
	panic("unimplemented")
}

func (m *machine) Pause() error {
	panic("unimplemented")
}

func (m *machine) Resume() error {
	panic("unimplemented")
}

func (m *machine) ConnectVsock(port uint32) (net.Conn, error) {
	panic("unimplemented")
}

func (m *machine) StateChan() <-chan vmm.MachineState {
	return stateChan
}

func (m *machine) Close() error {
	// if we try to get write lock, and ConnectVsock is hanging b/c VM is frozen,
	// then we'll wait forever. Instead, CAS the pointer.
	// Hacky but this seems like the best solution.
	// TODO: we could race in between when a ConnectVsock call got the pointer, and when Swift side took a ref
	m.mu.RLock()
	defer m.mu.RUnlock()

	// drop our long-lived ref, but don't delete the handle until Swift deinit's
	ptr := m.ptr.Swap(nil)
	if ptr != nil {
		C.rsvm_machine_destroy(ptr)
	}

	for _, f := range m.retainFiles {
		f.Close()
	}
	m.retainFiles = nil

	return nil
}
