package vzf

/*
#cgo CFLAGS: -mmacosx-version-min=12.3
#cgo LDFLAGS: -mmacosx-version-min=12.3 -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib/swift -L/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/lib/swift/macosx

#define CGO
#include "../../swift/GoVZF/GoVZF.h"

struct GovzfResultCreate* govzf_run_NewMachine(uintptr_t handle, const char* params_str);
struct GovzfResultErr* govzf_run_Machine_Start(void* ptr);
struct GovzfResultErr* govzf_run_Machine_Stop(void* ptr);
struct GovzfResultErr* govzf_run_Machine_RequestStop(void* ptr);
struct GovzfResultErr* govzf_run_Machine_Pause(void* ptr);
struct GovzfResultErr* govzf_run_Machine_Resume(void* ptr);
struct GovzfResultIntErr* govzf_run_Machine_ConnectVsock(void* ptr, uint32_t port);
void govzf_run_Machine_finalize(void* ptr);

char* swext_proxy_get_settings();
char* swext_proxy_monitor_changes();
*/
import (
	"C"
)
import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"runtime/cgo"
	"sync/atomic"
	"unsafe"

	"github.com/sirupsen/logrus"
)

type Machine struct {
	aPtr   unsafe.Pointer
	handle cgo.Handle

	retainFiles []*os.File

	stateChan chan MachineState
}

type MachineState int

// matches Swift enum
const (
	MachineStateStopped MachineState = iota
	MachineStateRunning
	MachineStatePaused
	MachineStateError
	MachineStateStarting
	MachineStatePausing
	MachineStateResuming
	// macOS 12
	MachineStateStopping
)

//export govzf_event_Machine_onStateChange
func govzf_event_Machine_onStateChange(vmHandle C.uintptr_t, state MachineState) {
	vm := cgo.Handle(vmHandle).Value().(*Machine)

	// no lock needed: channel never changes
	ch := vm.stateChan
	go func() {
		ch <- state
	}()
}

// Callback when Swift object is deinitialized. At this point, we know that nothing
// refers to the Cgo handle anymore, so we can delete it.
//
// Can't take lock because this can be called during Close().
//
//export govzf_event_Machine_deinit
func govzf_event_Machine_deinit(vmHandle C.uintptr_t) {
	vm := cgo.Handle(vmHandle).Value().(*Machine)

	cgo.Handle(vm.handle).Delete()
	vm.handle = 0
}

func NewMachine(spec VzSpec, retainFiles []*os.File) (*Machine, bool, error) {
	// encode to json
	specStr, err := json.Marshal(spec)
	if err != nil {
		return nil, false, err
	}

	// create Go object
	vm := &Machine{
		stateChan:   make(chan MachineState, 1),
		retainFiles: retainFiles,
	}
	handle := cgo.NewHandle(vm)
	vm.handle = handle

	// call cgo
	cstr := C.CString(string(specStr))
	defer C.free(unsafe.Pointer(cstr))
	result := C.govzf_run_NewMachine(C.uintptr_t(handle), cstr)
	defer C.free(unsafe.Pointer(result))

	// wait for result
	if result.err != nil {
		handle.Delete()
		return nil, bool(result.rosetta_canceled), errFromC(result.err)
	}

	// set ptr
	atomic.StorePointer(&vm.aPtr, result.ptr)
	// ref ok: this just drops Go ref; Swift ref is still held if alive
	runtime.SetFinalizer(vm, (*Machine).Close)

	return vm, bool(result.rosetta_canceled), nil
}

func (m *Machine) StateChan() <-chan MachineState {
	return m.stateChan
}

func errFromC(err *C.char) error {
	if err == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(err))
	return errors.New(C.GoString(err))
}

func (m *Machine) callGenericErr(fn func(unsafe.Pointer) *C.struct_GovzfResultErr) error {
	ptr := atomic.LoadPointer(&m.aPtr)
	if ptr == nil {
		return errors.New("machine closed")
	}

	res := fn(ptr)
	return errFromC(res.err)
}

func (m *Machine) callGenericErrInt(fn func(unsafe.Pointer) *C.struct_GovzfResultIntErr) (int64, error) {
	ptr := atomic.LoadPointer(&m.aPtr)
	if ptr == nil {
		return 0, errors.New("machine closed")
	}

	res := fn(ptr)
	return int64(res.value), errFromC(res.err)
}

func (m *Machine) Start() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) *C.struct_GovzfResultErr {
		return C.govzf_run_Machine_Start(ptr)
	})
}

func (m *Machine) Stop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) *C.struct_GovzfResultErr {
		return C.govzf_run_Machine_Stop(ptr)
	})
}

func (m *Machine) RequestStop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) *C.struct_GovzfResultErr {
		return C.govzf_run_Machine_RequestStop(ptr)
	})
}

func (m *Machine) Pause() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) *C.struct_GovzfResultErr {
		return C.govzf_run_Machine_Pause(ptr)
	})
}

func (m *Machine) Resume() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) *C.struct_GovzfResultErr {
		return C.govzf_run_Machine_Resume(ptr)
	})
}

func (m *Machine) ConnectVsock(port uint32) (net.Conn, error) {
	fd, err := m.callGenericErrInt(func(ptr unsafe.Pointer) *C.struct_GovzfResultIntErr {
		return C.govzf_run_Machine_ConnectVsock(ptr, C.uint32_t(port))
	})
	if err != nil {
		return nil, err
	}

	// unix socket
	file := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port))
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (m *Machine) Close() error {
	// drop our long-lived ref, but don't delete the handle until Swift deinit's
	ptr := atomic.SwapPointer(&m.aPtr, nil)
	if ptr != nil {
		C.govzf_run_Machine_finalize(ptr)
	}

	if len(m.retainFiles) > 0 {
		for _, f := range m.retainFiles {
			f.Close()
		}
		m.retainFiles = nil
	}

	return nil
}

func SwextProxyGetSettings() (*SwextProxySettings, error) {
	cStr := C.swext_proxy_get_settings()
	if cStr == nil {
		return nil, errors.New("swift returned nil")
	}

	// convert to Go
	var settings SwextProxySettings
	err := json.Unmarshal([]byte(C.GoString(cStr)), &settings)
	C.free(unsafe.Pointer(cStr))
	if err != nil {
		return nil, err
	}

	return &settings, nil
}

//export swext_proxy_cb_changed
func swext_proxy_cb_changed() {
	logrus.Debug("sys proxy settings changed")
	go func() {
		// defend against blocked subscribers
		SwextProxyChangesChan <- struct{}{}
	}()
}

func SwextProxyMonitorChangesOnRunLoop() error {
	msgC := C.swext_proxy_monitor_changes()
	msgStr := C.GoString(msgC)
	C.free(unsafe.Pointer(msgC))

	if msgStr != "" {
		return errors.New(msgStr)
	}

	return nil
}
