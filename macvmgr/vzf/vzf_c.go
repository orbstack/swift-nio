package vzf

/*
#cgo CFLAGS: -mmacosx-version-min=12.4
#cgo LDFLAGS: -mmacosx-version-min=12.4 -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib/swift -L/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/lib/swift/macosx

#include <stdlib.h>

void govzf_post_NewMachine(uintptr_t handle, const char* params_str);
void govzf_post_Machine_Start(void* ptr);
void govzf_post_Machine_Stop(void* ptr);
void govzf_post_Machine_RequestStop(void* ptr);
void govzf_post_Machine_Pause(void* ptr);
void govzf_post_Machine_Resume(void* ptr);
void govzf_post_Machine_ConnectVsock(void* ptr, uint32_t port);
void govzf_post_Machine_finalize(void* ptr);

char* swext_proxy_get_settings();
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
	"sync"
	"unsafe"

	"github.com/sirupsen/logrus"
)

type Machine struct {
	mu     sync.Mutex
	ptr    unsafe.Pointer
	handle cgo.Handle

	retainFiles []*os.File

	stateChan         chan MachineState
	createChan        chan<- newMachineResult
	genericErrChan    chan<- error
	genericErrIntChan chan<- errIntResult
}

type newMachineResult struct {
	cPtr            unsafe.Pointer
	err             error
	rosettaCanceled bool
}

type errIntResult struct {
	err   error
	value int64
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

//export govzf_complete_NewMachine
func govzf_complete_NewMachine(vmHandle C.uintptr_t, cPtr unsafe.Pointer, errC *C.char, rosettaCanceled bool) {
	errStr := C.GoString(errC)
	var err error
	if errStr != "" {
		err = errors.New(errStr)
	}

	// no lock needed: caller holds mutex
	vm := cgo.Handle(vmHandle).Value().(*Machine)
	if vm.createChan != nil {
		vm.createChan <- newMachineResult{cPtr, err, rosettaCanceled}
	} else {
		logrus.Error("[vzf] createChan = nil")
	}
}

//export govzf_complete_Machine_genericErr
func govzf_complete_Machine_genericErr(vmHandle C.uintptr_t, errC *C.char) {
	errStr := C.GoString(errC)
	var err error
	if errStr != "" {
		err = errors.New(errStr)
	}

	// no lock needed: caller holds mutex
	vm := cgo.Handle(vmHandle).Value().(*Machine)
	if vm.genericErrChan != nil {
		vm.genericErrChan <- err
	} else {
		logrus.Error("[vzf] genericErrChan = nil")
	}
}

//export govzf_complete_Machine_genericErrInt
func govzf_complete_Machine_genericErrInt(vmHandle C.uintptr_t, errC *C.char, value C.int64_t) {
	errStr := C.GoString(errC)
	var err error
	if errStr != "" {
		err = errors.New(errStr)
	}

	// no lock needed: caller holds mutex
	vm := cgo.Handle(vmHandle).Value().(*Machine)
	if vm.genericErrIntChan != nil {
		vm.genericErrIntChan <- errIntResult{err, int64(value)}
	} else {
		logrus.Error("[vzf] genericErrChan = nil")
	}
}

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

	// start create op
	ch := make(chan newMachineResult)
	vm.createChan = ch
	defer func() {
		vm.createChan = nil
	}()

	// call cgo
	cstr := C.CString(string(specStr))
	defer C.free(unsafe.Pointer(cstr))
	C.govzf_post_NewMachine(C.uintptr_t(handle), cstr)

	// wait for result
	result := <-ch
	if result.err != nil {
		handle.Delete()
		return nil, result.rosettaCanceled, result.err
	}

	// set ptr
	vm.ptr = result.cPtr
	// ref ok: this just drops Go ref; Swift ref is still held if alive
	runtime.SetFinalizer(vm, (*Machine).Close)

	return vm, result.rosettaCanceled, nil
}

func (m *Machine) StateChan() <-chan MachineState {
	return m.stateChan
}

func (m *Machine) callGenericErr(fn func(unsafe.Pointer)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptr == nil {
		return errors.New("machine closed")
	}

	ch := make(chan error)
	m.genericErrChan = ch
	defer func() {
		m.genericErrChan = nil
	}()
	fn(m.ptr)
	res := <-ch
	return res
}

func (m *Machine) callGenericErrInt(fn func(unsafe.Pointer)) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptr == nil {
		return 0, errors.New("machine closed")
	}

	ch := make(chan errIntResult)
	m.genericErrIntChan = ch
	defer func() {
		m.genericErrIntChan = nil
	}()
	fn(m.ptr)
	res := <-ch
	return res.value, res.err
}

func (m *Machine) Start() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) {
		C.govzf_post_Machine_Start(ptr)
	})
}

func (m *Machine) Stop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) {
		C.govzf_post_Machine_Stop(ptr)
	})
}

func (m *Machine) RequestStop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) {
		C.govzf_post_Machine_RequestStop(ptr)
	})
}

func (m *Machine) Pause() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) {
		C.govzf_post_Machine_Pause(ptr)
	})
}

func (m *Machine) Resume() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) {
		C.govzf_post_Machine_Resume(ptr)
	})
}

func (m *Machine) ConnectVsock(port uint32) (net.Conn, error) {
	fd, err := m.callGenericErrInt(func(ptr unsafe.Pointer) {
		C.govzf_post_Machine_ConnectVsock(ptr, C.uint32_t(port))
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
	m.mu.Lock()
	defer m.mu.Unlock()

	// drop our long-lived ref, but don't delete the handle until Swift deinit's
	if m.ptr != nil {
		C.govzf_post_Machine_finalize(m.ptr)
		m.ptr = nil
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
