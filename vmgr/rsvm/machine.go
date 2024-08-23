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
struct GResultErr rsvm_machine_dump_debug(void* ptr);
struct GResultErr rsvm_machine_start_profile(void* ptr, const char* params_json_str, size_t params_len);
struct GResultErr rsvm_machine_stop_profile(void* ptr);
struct GResultErr rsvm_machine_stop(void* ptr);
void rsvm_machine_destroy(void* ptr);

struct GResultErr rsvm_set_rinit_data(const char* ptr, size_t len);
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
	"golang.org/x/sys/unix"
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
	ptr    unsafe.Pointer
	handle cgo.Handle

	retainFiles []*os.File
}

func (m monitor) NewMachine(spec *vmm.VzSpec, retainFiles []*os.File) (vmm.Machine, error) {
	// HVF limitation: VM is a process-global resource
	if !vmCreated.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("only one VM can be created in a process")
	}

	// to work correctly, krun requires console fds to be non-blocking
	if spec.Console != nil {
		unix.SetNonblock(spec.Console.ReadFd, true)
		unix.SetNonblock(spec.Console.WriteFd, true)
	}

	// Rust println and tracing_subscriber (via eprint) panics if writing to stderr fails
	// libkrun always writes to stdio for logging, so we need to take the fd out of non-blocking
	// (*os.File).Fd() is supposed to do that, but it doesn't seem to work here, so we need to do it manually
	// at first glance this is bad if we're logging to a file, but nonblock doesn't work on files anyway, so it was already blocking; this only affects debug (pty output)
	unix.SetNonblock(int(os.Stderr.Fd()), false)

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
	vm.ptr = result.ptr
	// ref ok: this just drops Go ref; Swift ref is still held if alive
	runtime.SetFinalizer(vm, (*machine).Close)

	return vm, nil
}

func (m *machine) callGenericErr(fn func(unsafe.Pointer) C.struct_GResultErr) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ptr == nil {
		return errors.New("machine closed")
	}

	res := fn(m.ptr)
	return errFromC(res.err)
}

func (m *machine) callGenericErrInt(fn func(unsafe.Pointer) C.struct_GResultIntErr) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ptr == nil {
		return 0, errors.New("machine closed")
	}

	res := fn(m.ptr)
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
	return errors.New("unimplemented")
}

func (m *machine) Pause() error {
	return errors.New("unimplemented")
}

func (m *machine) Resume() error {
	return errors.New("unimplemented")
}

func (m *machine) ConnectVsock(port uint32) (net.Conn, error) {
	return nil, errors.New("unimplemented")
}

func (m *machine) StateChan() <-chan vmm.MachineState {
	return stateChan
}

func (m *machine) DumpDebug() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.rsvm_machine_dump_debug(ptr)
	})
}

func (m *machine) StartProfile(params *vmm.ProfilerParams) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}

	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.rsvm_machine_start_profile(ptr, (*C.char)(unsafe.Pointer(&data[0])), C.size_t(len(data)))
	})
}

func (m *machine) StopProfile() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.rsvm_machine_stop_profile(ptr)
	})
}

func (m *machine) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// drop our long-lived ref
	if m.ptr != nil {
		C.rsvm_machine_destroy(m.ptr)
		m.ptr = nil
	} else {
		// already closed
		return nil
	}

	// Rust side isn't Arc, so we can also delete the handle
	m.handle.Delete()

	for _, f := range m.retainFiles {
		f.Close()
	}
	m.retainFiles = nil

	return nil
}

func SetRinitData(data []byte) error {
	res := C.rsvm_set_rinit_data(C.CString(string(data)), C.size_t(len(data)))
	return errFromC(res.err)
}
