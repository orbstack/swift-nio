package vzf

/*
#cgo LDFLAGS: -framework Foundation -framework Virtualization -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib/swift

#include <stdlib.h>

void govzf_post_NewMachine(uintptr_t handle, const char* params_str);
void govzf_post_Machine_Start(void* ptr);
void govzf_post_Machine_Stop(void* ptr);
void govzf_post_Machine_RequestStop(void* ptr);
void govzf_post_Machine_Pause(void* ptr);
void govzf_post_Machine_Resume(void* ptr);
void govzf_post_Machine_ConnectVsock(void* ptr, uint32_t port);
void govzf_post_Machine_finalize(void* ptr);
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
	mu  sync.Mutex
	ptr unsafe.Pointer

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
	fmt.Println("[vzf] govzf_complete_NewMachine", vmHandle, cPtr, errC, rosettaCanceled)
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
	fmt.Println("[vzf] govzf_complete_Machine_genericErr", vmHandle, errC)
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

	fmt.Println("[vzf] govzf_complete_Machine_genericErr done")
}

//export govzf_complete_Machine_genericErrInt
func govzf_complete_Machine_genericErrInt(vmHandle C.uintptr_t, errC *C.char, value C.int64_t) {
	fmt.Println("[vzf] govzf_complete_Machine_genericErrInt", vmHandle, errC)
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

	fmt.Println("[vzf] govzf_complete_Machine_genericErrInt done")
}

//export govzf_event_Machine_onStateChange
func govzf_event_Machine_onStateChange(vmHandle C.uintptr_t, state MachineState) {
	fmt.Println("[vzf] govzf_event_Machine_onStateChange", vmHandle, state)
	vm := cgo.Handle(vmHandle).Value().(*Machine)

	// no lock needed: channel never changes
	ch := vm.stateChan
	go func() {
		ch <- state
	}()

	fmt.Println("[vzf] govzf_event_Machine_onStateChange done")
}

func NewMachine(spec VzSpec) (*Machine, bool, error) {
	// encode to json
	specStr, err := json.Marshal(spec)
	if err != nil {
		return nil, false, err
	}

	// setup
	vm := &Machine{
		stateChan: make(chan MachineState, 1),
	}
	ch := make(chan newMachineResult)
	vm.createChan = ch
	defer func() {
		vm.createChan = nil
	}()
	handle := cgo.NewHandle(vm)

	// call cgo
	cstr := C.CString(string(specStr))
	defer C.free(unsafe.Pointer(cstr))
	fmt.Println("calling cgo")
	C.govzf_post_NewMachine(C.uintptr_t(handle), cstr)
	fmt.Println("called cgo")

	// wait for result
	fmt.Println("waiting for result")
	result := <-ch
	fmt.Println("got result", result)
	if result.err != nil {
		handle.Delete()
		return nil, result.rosettaCanceled, result.err
	}

	// set ptr
	vm.ptr = result.cPtr

	// set finalizer
	//TODO this will never run because handle
	runtime.SetFinalizer(vm, func(vm *Machine) {
		fmt.Println("finalizer called")
		vm.mu.Lock()
		defer vm.mu.Unlock()
		if vm.ptr != nil {
			C.govzf_post_Machine_finalize(vm.ptr)
			vm.ptr = nil
		}

		//TODO is this safe?
		handle.Delete()
	})

	return vm, result.rosettaCanceled, nil
}

func (m *Machine) StateChan() <-chan MachineState {
	return m.stateChan
}

func (m *Machine) callGenericErr(fn func(unsafe.Pointer)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptr == nil {
		return errors.New("machine destroyed")
	}

	fmt.Println("callGenericErr called")
	ch := make(chan error)
	m.genericErrChan = ch
	defer func() {
		m.genericErrChan = nil
	}()
	fmt.Println("calling fn")
	fn(m.ptr)
	fmt.Println("called fn, wait")
	res := <-ch
	fmt.Println("got res", res)
	return res
}

func (m *Machine) callGenericErrInt(fn func(unsafe.Pointer)) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptr == nil {
		return 0, errors.New("machine destroyed")
	}

	fmt.Println("callGenericErr called")
	ch := make(chan errIntResult)
	m.genericErrIntChan = ch
	defer func() {
		m.genericErrIntChan = nil
	}()
	fmt.Println("calling fn")
	fn(m.ptr)
	fmt.Println("called fn, wait")
	res := <-ch
	fmt.Println("got res", res)
	return res.value, res.err
}

func (m *Machine) Start() error {
	fmt.Println("start called")
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
	return errors.New("not implemented")
}
