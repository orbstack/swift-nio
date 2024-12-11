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
 *   - Rust code (libvirtue) doesn't currently spawn any processes
 *   - Swift code (GoVZF) uses NSTask, which uses posix_spawn
 *   - Not aware of any Go libraries using exec.Command or os.StartProcess (should check this... or better, make it panic somehow?)
 * ... but we must still use syscall.ForkLock for CLOEXEC race safety, because there's one remaining user of exec.Command: hostssh server. It needs setctty, so we could only use posix_spawn if we use a helper executable that handles setctty.
 */

package pspawn

/*
#include <spawn.h>
#include <stdlib.h>
#include <signal.h>
#include <errno.h>

extern int responsibility_spawnattrs_setdisclaim(posix_spawnattr_t *attrs, int disclaim) __attribute__((weak_import));

int orb_pspawn_setdisclaim(posix_spawnattr_t *attrs, int disclaim) {
	if (responsibility_spawnattrs_setdisclaim) {
		return responsibility_spawnattrs_setdisclaim(attrs, disclaim);
	} else {
		return ENOTSUP;
	}
}
*/
import "C"
import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"unsafe"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type PspawnAttr struct {
	// DisclaimTCCResponsibility gives the child process a new TCC context based on its
	// signing ID, disclaiming responsibility for its syscalls. (pspawn-specific)
	DisclaimTCCResponsibility bool
}

func StartProcess(exe string, argv []string, attr *os.ProcAttr, pspawnAttr *PspawnAttr) (*os.Process, error) {
	// use "pstramp" trampoline to handle calls that aren't supported by posix_spawn
	// this only includes setctty for now
	if attr != nil && attr.Sys != nil && attr.Sys.Setctty {
		pstrampExe, err := conf.FindPstrampExe()
		if err != nil {
			return nil, err
		}

		argv = append([]string{pstrampExe, "-setctty", strconv.Itoa(int(attr.Sys.Ctty)), "--", exe}, argv...)
		exe = pstrampExe
	}

	return startProcessRaw(exe, argv, attr, pspawnAttr)
}

func startProcessRaw(exe string, argv []string, attr *os.ProcAttr, pspawnAttr *PspawnAttr) (*os.Process, error) {
	// don't close files
	defer runtime.KeepAlive(attr)

	env := attr.Env
	if env == nil {
		env = unix.Environ()
	}

	// this is safe: cgo implicitly pins args for duration of call, and this only passed to posix_spawn
	exeC, err := unix.BytePtrFromString(exe)
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
		return nil, fmt.Errorf("posix_spawnattr_init: %w", unix.Errno(ret))
	}
	defer C.posix_spawnattr_destroy(&spawnattr)

	spawnFlags := C.POSIX_SPAWN_CLOEXEC_DEFAULT | C.POSIX_SPAWN_SETSIGDEF | C.POSIX_SPAWN_SETSIGMASK
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
				return nil, fmt.Errorf("posix_spawnattr_setpgroup: %w", unix.Errno(ret))
			}
		}

		// Setctty is handled in StartProcess

		if attr.Sys.Noctty {
			return nil, errors.New("noctty not supported")
		}

		if attr.Sys.Foreground {
			return nil, errors.New("foreground not supported")
		}
	}
	ret = C.posix_spawnattr_setflags(&spawnattr, C.short(spawnFlags))
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawnattr_setflags: %w", unix.Errno(ret))
	}

	if pspawnAttr != nil {
		if pspawnAttr.DisclaimTCCResponsibility {
			ret = C.orb_pspawn_setdisclaim(&spawnattr, 1)
			if ret != 0 {
				err := unix.Errno(ret)
				if err == unix.ENOTSUP {
					logrus.Warn("disclaiming TCC responsibility is not supported on this OS version")
				} else {
					return nil, fmt.Errorf("responsibility_spawnattrs_setdisclaim: %w", unix.Errno(ret))
				}
			}
		}
	}

	// reset all signal actions to default, and unmask all signals
	var sigset C.sigset_t
	C.sigfillset(&sigset)
	ret = C.posix_spawnattr_setsigdefault(&spawnattr, &sigset)
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawnattr_setsigdefault: %w", unix.Errno(ret))
	}
	C.sigemptyset(&sigset)
	ret = C.posix_spawnattr_setsigmask(&spawnattr, &sigset)
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawnattr_setsigmask: %w", unix.Errno(ret))
	}

	var fileActions C.posix_spawn_file_actions_t
	ret = C.posix_spawn_file_actions_init(&fileActions)
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawn_file_actions_init: %w", unix.Errno(ret))
	}
	defer C.posix_spawn_file_actions_destroy(&fileActions)

	// chdir
	if attr.Dir != "" {
		// be safe here
		dirC := C.CString(attr.Dir)
		defer C.free(unsafe.Pointer(dirC))

		ret := C.posix_spawn_file_actions_addchdir_np(&fileActions, dirC)
		if ret != 0 {
			return nil, fmt.Errorf("posix_spawn_file_actions_addchdir: %w", unix.Errno(ret))
		}
	}

	// files: stdin, stdout, stderr, etc.
	for i, file := range attr.Files {
		// .Fd() sets to non-blocking
		ret := C.posix_spawn_file_actions_adddup2(&fileActions, C.int(file.Fd()), C.int(i))
		if ret != 0 {
			return nil, fmt.Errorf("posix_spawn_file_actions_adddup2: %w", unix.Errno(ret))
		}
	}

	var pid C.pid_t
	ret = C.posix_spawn(&pid, (*C.char)(unsafe.Pointer(exeC)), &fileActions, &spawnattr, (**C.char)(unsafe.Pointer(&argvC[0])), (**C.char)(unsafe.Pointer(&envvC[0])))
	if ret != 0 {
		return nil, fmt.Errorf("posix_spawn: %w", unix.Errno(ret))
	}

	return os.FindProcess(int(pid))
}
