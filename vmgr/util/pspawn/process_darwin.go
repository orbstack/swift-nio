//go:build darwin
/*
 * Most of this package (exec.Command wrappers) is copied from Go 1.21.5 stdlib.
 * We just implement an os.StartProcess() equivalent using posix_spawn.
 *
 * Advantages:
 *   - faster than fork+exec. *significantly* faster when the process has large memory mappings, e.g. 64 GiB of hv_vm_allocate (mach_vm_map), where inheritance can take 1-2 sec. the parent process' memory space is locked for the entire duration of the fork, causing malloc to get stuck on ulock for a long time, ultimately causing long stalls in several parent threads.
 *   - FD safety: CLOEXEC_DEFAULT means we that don't have to worry about leaking FDs. it's not only about forgetting to set CLOEXEC: it's impossible to do race-free
 *
 * in terms of the entire vmgr process:
 *   - Rust code (libkrun) doesn't currently spawn any processes
 *   - Swift code (GoVZF) uses NSTask, which uses posix_spawn
 *   - Not aware of any Go libraries using exec.Command or os.StartProcess (should check this... or better, make it panic somehow?)
 * ... but we must still use syscall.ForkLock for CLOEXEC race safety, because there's one remaining user of exec.Command: hostssh server. It needs setctty, so we could only use posix_spawn if we use a helper executable that handles setctty.
 */

package pspawn

/*
#include <spawn.h>
#include <stdlib.h>
*/
import "C"
import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func StartProcess(exe string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
	// don't close files
	defer runtime.KeepAlive(attr)

	env := attr.Env
	if env == nil {
		env = syscall.Environ()
	}

	// this is safe: cgo implicitly pins args for duration of call, and this only passed to posix_spawn
	exeC, err := syscall.BytePtrFromString(exe)
	if err != nil {
		return nil, err
	}
	// let's be safe for these. pinning slices is only OK if they contain no Go pointers
	argvC := make([]*C.char, len(argv)+1)
	for i, arg := range argv {
		argvC[i] = C.CString(arg)
		defer C.free(unsafe.Pointer(argvC[i]))
	}
	envvC := make([]*C.char, len(env)+1)
	for i, env := range env {
		envvC[i] = C.CString(env)
		defer C.free(unsafe.Pointer(envvC[i]))
	}

	var spawnattr C.posix_spawnattr_t
	ret := C.posix_spawnattr_init(&spawnattr)
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawnattr_init: %w", syscall.Errno(ret))
	}
	defer C.posix_spawnattr_destroy(&spawnattr)

	spawnFlags := C.POSIX_SPAWN_CLOEXEC_DEFAULT
	if attr.Sys != nil {
		if attr.Sys.Chroot != "" {
			return nil, errors.New("chroot not supported")
		}

		if attr.Sys.Credential != nil {
			return nil, errors.New("credential not supported")
		}

		if attr.Sys.Ptrace {
			return nil, errors.New("ptrace not supported")
		}

		if attr.Sys.Setsid {
			spawnFlags |= C.POSIX_SPAWN_SETSID
		}

		if attr.Sys.Setpgid {
			spawnFlags |= C.POSIX_SPAWN_SETPGROUP
			ret := C.posix_spawnattr_setpgroup(&spawnattr, C.int(attr.Sys.Pgid))
			if ret != 0 {
				return nil, fmt.Errorf("posix_spawnattr_setpgroup: %w", syscall.Errno(ret))
			}
		}

		if attr.Sys.Setctty {
			return nil, errors.New("setctty not supported")
		}

		if attr.Sys.Noctty {
			return nil, errors.New("noctty not supported")
		}

		if attr.Sys.Foreground {
			return nil, errors.New("foreground not supported")
		}
	}
	ret = C.posix_spawnattr_setflags(&spawnattr, C.short(spawnFlags))
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawnattr_setflags: %w", syscall.Errno(ret))
	}

	var fileActions C.posix_spawn_file_actions_t
	ret = C.posix_spawn_file_actions_init(&fileActions)
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawn_file_actions_init: %w", syscall.Errno(ret))
	}
	defer C.posix_spawn_file_actions_destroy(&fileActions)

	// chdir
	if attr.Dir != "" {
		// be safe here
		dirC := C.CString(attr.Dir)
		defer C.free(unsafe.Pointer(dirC))

		ret := C.posix_spawn_file_actions_addchdir_np(&fileActions, dirC)
		if ret != 0 {
			return nil, fmt.Errorf("posix_spawn_file_actions_addchdir: %w", syscall.Errno(ret))
		}
	}

	// files: stdin, stdout, stderr, etc.
	for i, file := range attr.Files {
		// .Fd() sets to non-blocking
		ret := C.posix_spawn_file_actions_adddup2(&fileActions, C.int(file.Fd()), C.int(i))
		if ret != 0 {
			return nil, fmt.Errorf("posix_spawn_file_actions_adddup2: %w", syscall.Errno(ret))
		}
	}

	var pid C.pid_t
	ret = C.posix_spawn(&pid, (*C.char)(unsafe.Pointer(exeC)), &fileActions, &spawnattr, (**C.char)(unsafe.Pointer(&argvC[0])), (**C.char)(unsafe.Pointer(&envvC[0])))
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawn: %w", syscall.Errno(ret))
	}

	return os.FindProcess(int(pid))
}
