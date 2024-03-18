package vzf

/*
#cgo CFLAGS: -mmacosx-version-min=12.3
#cgo LDFLAGS: -mmacosx-version-min=12.3 -L/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/lib/swift -L/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/lib/swift/macosx

#define CGO
#include "../../swift/GoVZF/Sources/CBridge/CBridge.h"

struct GResultCreate govzf_run_NewMachine(uintptr_t handle, const char* config_json_str);
struct GResultErr govzf_run_Machine_Start(void* ptr);
struct GResultErr govzf_run_Machine_Stop(void* ptr);
struct GResultErr govzf_run_Machine_RequestStop(void* ptr);
struct GResultErr govzf_run_Machine_Pause(void* ptr);
struct GResultErr govzf_run_Machine_Resume(void* ptr);
struct GResultIntErr govzf_run_Machine_ConnectVsock(void* ptr, uint32_t port);
void govzf_run_Machine_finalize(void* ptr);

struct GResultIntErr swext_install_rosetta();

char* swext_proxy_get_settings(bool need_auth);
struct GResultErr swext_proxy_monitor_changes(void);

char* swext_security_get_extra_ca_certs(void);
struct GResultErr swext_security_import_certificate(const char* cert_der_b64);

char* swext_fsevents_monitor_dirs(void);
void* swext_fsevents_VmNotifier_new(void);
struct GResultErr swext_fsevents_VmNotifier_start(void* ptr);
struct GResultErr swext_fsevents_VmNotifier_updatePaths(void* ptr, const char** paths, int count);
void swext_fsevents_VmNotifier_stop(void* ptr);
void swext_fsevents_VmNotifier_finalize(void* ptr);
void swext_ipc_notify_uievent(const char* event);

struct GResultCreate swext_brnet_create(const char* config_json_str);
void swext_brnet_close(void* ptr);

char* swext_defaults_get_user_settings(void);

void* swext_vlanrouter_new(const char* config_json_str);
struct GResultIntErr swext_vlanrouter_addBridge(void* ptr, const char* config_json_str);
struct GResultErr swext_vlanrouter_removeBridge(void* ptr, int index);
struct GResultErr swext_vlanrouter_renewBridge(void* ptr, int index, const char* config_json_str);
void swext_vlanrouter_clearBridges(void* ptr);
void swext_vlanrouter_close(void* ptr);

struct GResultErr swext_gui_run_as_admin(char* shell_script, char* prompt);

struct GResultErr swext_privhelper_symlink(const char* src, const char* dst);
struct GResultErr swext_privhelper_uninstall(void);
void swext_privhelper_set_install_reason(const char* reason);
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

	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/sirupsen/logrus"
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

/*
 * Virtual Machine
 */

type machine struct {
	mu     sync.RWMutex
	ptr    atomicUnsafePointer
	handle cgo.Handle

	retainFiles []*os.File

	stateChan chan vmm.MachineState
}

//export govzf_event_Machine_onStateChange
func govzf_event_Machine_onStateChange(vmHandle C.uintptr_t, state int) {
	vm := cgo.Handle(vmHandle).Value().(*machine)

	// no lock needed: channel never changes
	ch := vm.stateChan
	go func() {
		ch <- vmm.MachineState(state)
	}()
}

// Callback when Swift object is deinitialized. At this point, we know that nothing
// refers to the Cgo handle anymore, so we can delete it.
//
// Can't take lock because this can be called during Close().
//
//export govzf_event_Machine_deinit
func govzf_event_Machine_deinit(vmHandle C.uintptr_t) {
	vm := cgo.Handle(vmHandle).Value().(*machine)

	cgo.Handle(vm.handle).Delete()
	vm.handle = 0
}

func (m monitor) NewMachine(spec *vmm.VzSpec, retainFiles []*os.File) (vmm.Machine, error) {
	// encode to json
	specStr, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}

	// create Go object
	vm := &machine{
		stateChan:   make(chan vmm.MachineState, 1),
		retainFiles: retainFiles,
	}
	handle := cgo.NewHandle(vm)
	vm.handle = handle

	// call cgo
	cstr := C.CString(string(specStr))
	defer C.free(unsafe.Pointer(cstr))
	result := C.govzf_run_NewMachine(C.uintptr_t(handle), cstr)

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

func (m *machine) StateChan() <-chan vmm.MachineState {
	return m.stateChan
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
		return C.govzf_run_Machine_Start(ptr)
	})
}

func (m *machine) ForceStop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.govzf_run_Machine_Stop(ptr)
	})
}

func (m *machine) RequestStop() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.govzf_run_Machine_RequestStop(ptr)
	})
}

func (m *machine) Pause() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.govzf_run_Machine_Pause(ptr)
	})
}

func (m *machine) Resume() error {
	return m.callGenericErr(func(ptr unsafe.Pointer) C.struct_GResultErr {
		return C.govzf_run_Machine_Resume(ptr)
	})
}

func (m *machine) ConnectVsock(port uint32) (net.Conn, error) {
	fd, err := m.callGenericErrInt(func(ptr unsafe.Pointer) C.struct_GResultIntErr {
		return C.govzf_run_Machine_ConnectVsock(ptr, C.uint32_t(port))
	})
	if err != nil {
		return nil, err
	}

	// unix socket
	file := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", port))
	defer file.Close() // that's a dup - we already dup'd in Swift
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}

	return conn, nil
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

/*
 * Rosetta
 */

type RosettaStatus int

const (
	RosettaStatusUnsupported RosettaStatus = iota
	RosettaStatusInstalled
	RosettaStatusInstallCanceled
)

func SwextInstallRosetta() (RosettaStatus, error) {
	res := C.swext_install_rosetta()
	return RosettaStatus(res.value), errFromC(res.err)
}

/*
 * Proxy
 */

func SwextProxyGetSettings(needAuth bool) (*SwextProxySettings, error) {
	cStr := C.swext_proxy_get_settings(C.bool(needAuth))
	defer C.free(unsafe.Pointer(cStr))
	str := C.GoString(cStr)

	// convert to Go
	var settings SwextProxySettings
	err := json.Unmarshal([]byte(str), &settings)
	if err != nil {
		return nil, err
	}

	return &settings, nil
}

//export swext_proxy_cb_changed
func swext_proxy_cb_changed() {
	logrus.Debug("sys proxy settings changed")
	// non-blocking send w/ adaptive 1-buf
	select {
	case SwextProxyChangesChan <- struct{}{}:
	default:
	}
}

func SwextProxyMonitorChangesOnRunLoop() error {
	res := C.swext_proxy_monitor_changes()
	return errFromResult(res)
}

/*
 * Security / certs
 */

func SwextSecurityGetExtraCaCerts() ([]string, error) {
	cStr := C.swext_security_get_extra_ca_certs()
	defer C.free(unsafe.Pointer(cStr))
	str := C.GoString(cStr)

	// error?
	if str[0] == 'E' {
		return nil, errors.New(str[1:])
	}

	// convert to Go
	var certs []string
	err := json.Unmarshal([]byte(str), &certs)
	if err != nil {
		return nil, err
	}

	return certs, nil
}

func SwextSecurityImportCertificate(certDerB64 string) error {
	cStr := C.CString(certDerB64)
	defer C.free(unsafe.Pointer(cStr))

	res := C.swext_security_import_certificate(cStr)
	return errFromResult(res)
}

/*
 * fsnotify
 */

//export swext_fsevents_cb_krpc_events
func swext_fsevents_cb_krpc_events(ptr *C.uint8_t, len C.size_t) {
	// copy to Go slice
	data := C.GoBytes(unsafe.Pointer(ptr), C.int(len))

	// send to channel
	// block if necessary for backpressure
	SwextFseventsKrpcEventsChan <- data
}

func SwextFseventsMonitorDirs() error {
	msgC := C.swext_fsevents_monitor_dirs()
	defer C.free(unsafe.Pointer(msgC))
	msgStr := C.GoString(msgC)

	if msgStr != "" {
		return errors.New(msgStr)
	}

	return nil
}

type FsVmNotifier struct {
	mu  sync.Mutex
	ptr unsafe.Pointer
}

func NewFsVmNotifier() (*FsVmNotifier, error) {
	ptr := C.swext_fsevents_VmNotifier_new()
	if ptr == nil {
		return nil, errors.New("failed to create FsVmNotifier")
	}

	notifier := &FsVmNotifier{
		ptr: ptr,
	}
	runtime.SetFinalizer(notifier, func(n *FsVmNotifier) {
		n.Close()
	})

	return notifier, nil
}

func (n *FsVmNotifier) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ptr != nil {
		C.swext_fsevents_VmNotifier_finalize(n.ptr)
		n.ptr = nil
		runtime.SetFinalizer(n, nil)
	}

	return nil
}

func (n *FsVmNotifier) Start() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ptr == nil {
		return errors.New("FsVmNotifier closed")
	}

	res := C.swext_fsevents_VmNotifier_start(n.ptr)
	return errFromResult(res)
}

func (n *FsVmNotifier) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ptr == nil {
		return errors.New("FsVmNotifier closed")
	}

	C.swext_fsevents_VmNotifier_stop(n.ptr)
	return nil
}

func (n *FsVmNotifier) UpdatePaths(paths []string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.ptr == nil {
		return errors.New("FsVmNotifier closed")
	}

	// min 1 for fsevents to work (also &cPaths[0] below)
	if len(paths) == 0 {
		paths = []string{"/.__non_existent_path__/.xyz"}
	}

	// convert to C
	cPaths := make([]*C.char, len(paths))
	for i, path := range paths {
		cPaths[i] = C.CString(path)
		defer C.free(unsafe.Pointer(cPaths[i]))
	}

	res := C.swext_fsevents_VmNotifier_updatePaths(n.ptr, &cPaths[0], C.int(len(paths)))
	return errFromResult(res)
}

/*
 * Notify
 */

func SwextIpcNotifyUIEvent(ev uitypes.UIEvent) {
	eventJson, err := json.Marshal(ev)
	if err != nil {
		logrus.WithError(err).Error("failed to marshal event")
		return
	}

	SwextIpcNotifyUIEventRaw(string(eventJson))
}

// raw is for more efficient sending from VM, via gob rpc
func SwextIpcNotifyUIEventRaw(eventJsonStr string) {
	logrus.Debug("sending UI event")

	cStr := C.CString(eventJsonStr)
	defer C.free(unsafe.Pointer(cStr))
	C.swext_ipc_notify_uievent(cStr)
}

/*
 * Defaults
 */

func SwextDefaultsGetUserSettings() (*SwextUserSettings, error) {
	cStr := C.swext_defaults_get_user_settings()
	defer C.free(unsafe.Pointer(cStr))
	str := C.GoString(cStr)

	// error?
	if str[0] == 'E' {
		return nil, errors.New(str[1:])
	}

	// convert to Go
	var settings SwextUserSettings
	err := json.Unmarshal([]byte(str), &settings)
	if err != nil {
		return nil, err
	}

	return &settings, nil
}

/*
 * BridgeNetwork
 */

type BridgeNetwork struct {
	mu  sync.RWMutex
	ptr unsafe.Pointer
}

func SwextNewBrnet(config BridgeNetworkConfig) (*BridgeNetwork, error) {
	// encode to json
	specStr, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	// create Go object
	brnet := &BridgeNetwork{}

	// call cgo
	cstr := C.CString(string(specStr))
	defer C.free(unsafe.Pointer(cstr))
	result := C.swext_brnet_create(cstr)

	// wait for result
	if result.err != nil {
		return nil, errFromC(result.err)
	}

	// set ptr
	brnet.ptr = result.ptr
	// ref ok: this just drops Go ref; Swift ref is still held if alive
	runtime.SetFinalizer(brnet, (*BridgeNetwork).Close)

	return brnet, nil
}

func (brnet *BridgeNetwork) Close() error {
	brnet.mu.Lock()
	defer brnet.mu.Unlock()

	if brnet.ptr != nil {
		C.swext_brnet_close(brnet.ptr)
		brnet.ptr = nil
		runtime.SetFinalizer(brnet, nil)
	}

	return nil
}

/*
 * VlanRouter
 */

type VlanRouter struct {
	mu  sync.RWMutex
	ptr unsafe.Pointer
}

func SwextNewVlanRouter(config VlanRouterConfig) (*VlanRouter, error) {
	// encode to json
	configStr, err := json.Marshal(&config)
	if err != nil {
		return nil, err
	}

	cstr := C.CString(string(configStr))
	defer C.free(unsafe.Pointer(cstr))
	ptr := C.swext_vlanrouter_new(cstr)
	if ptr == nil {
		return nil, errors.New("create failed")
	}

	router := &VlanRouter{
		ptr: ptr,
	}

	runtime.SetFinalizer(router, func(r *VlanRouter) {
		r.Close()
	})

	return router, nil
}

func (router *VlanRouter) AddBridge(config BridgeNetworkConfig) (int, error) {
	// encode to json
	configStr, err := json.Marshal(&config)
	if err != nil {
		return 0, err
	}

	router.mu.RLock()
	defer router.mu.RUnlock()

	if router.ptr == nil {
		return 0, errors.New("router closed")
	}

	// call cgo
	cstr := C.CString(string(configStr))
	defer C.free(unsafe.Pointer(cstr))
	result := C.swext_vlanrouter_addBridge(router.ptr, cstr)

	return int(result.value), errFromC(result.err)
}

func (router *VlanRouter) RemoveBridge(index int) error {
	router.mu.RLock()
	defer router.mu.RUnlock()

	if router.ptr == nil {
		return errors.New("router closed")
	}

	result := C.swext_vlanrouter_removeBridge(router.ptr, C.int(index))
	return errFromC(result.err)
}

func (router *VlanRouter) RenewBridge(index int) error {
	router.mu.RLock()
	defer router.mu.RUnlock()

	if router.ptr == nil {
		return errors.New("router closed")
	}

	result := C.swext_vlanrouter_renewBridge(router.ptr, C.int(index), nil)
	return errFromC(result.err)
}

func (router *VlanRouter) ClearBridges() error {
	router.mu.RLock()
	defer router.mu.RUnlock()

	if router.ptr == nil {
		return errors.New("router closed")
	}

	C.swext_vlanrouter_clearBridges(router.ptr)
	return nil
}

func (router *VlanRouter) Close() error {
	router.mu.Lock()
	defer router.mu.Unlock()

	if router.ptr != nil {
		C.swext_vlanrouter_close(router.ptr)
		router.ptr = nil
		runtime.SetFinalizer(router, nil)
	}

	return nil
}

//export swext_net_cb_path_changed
func swext_net_cb_path_changed() {
	logrus.Debug("sys net path changed")
	// non-blocking send w/ adaptive 1-buf
	select {
	case SwextNetPathChangesChan <- struct{}{}:
	default:
	}
}

/*
 * GUI
 */
func SwextGuiRunAsAdmin(shellScript string, prompt string) error {
	cShellScript := C.CString(shellScript)
	defer C.free(unsafe.Pointer(cShellScript))
	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))
	res := C.swext_gui_run_as_admin(cShellScript, cPrompt)
	return errFromResult(res)
}

/*
 * Priv helper
 */
func SwextPrivhelperSymlink(src string, dst string) error {
	cSrc := C.CString(src)
	defer C.free(unsafe.Pointer(cSrc))
	cDst := C.CString(dst)
	defer C.free(unsafe.Pointer(cDst))
	res := C.swext_privhelper_symlink(cSrc, cDst)
	return errFromResult(res)
}

func SwextPrivhelperUninstall() error {
	res := C.swext_privhelper_uninstall()
	return errFromResult(res)
}

func SwextPrivhelperSetInstallReason(reason string) {
	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))
	C.swext_privhelper_set_install_reason(cReason)
}
