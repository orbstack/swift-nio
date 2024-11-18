/*
 * forkaudit: crash on calls to fork(2) in debug builds
 *
 * It's impossible to use fork() safely in vmgr because macOS doesn't provide a way to set O_CLOEXEC atomically on all new fds.
 * The Go runtime can mostly work around this using syscall.ForkLock, but Rust, C, or Swift code can't cooperate with that.
 * So, to prevent security issues, hangs (if signaling/child pipes get leaked), and other bad behavior, all code in the vmgr process must use posix_spawn() instead of fork(). This allows force-defaulting O_CLOEXEC behavior on all fds that aren't explicitly inherited.
 *
 * This provides a way to audit compliance with the process-wide posix_spawn policy by aborting on fork().
 */

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

#define DEFINE_FN(ret_type, fn, ...) \
    typedef ret_type (*_real_##fn##_t)(__VA_ARGS__); \
    static _real_##fn##_t _real_##fn = NULL; \
    __attribute__((visibility("default"))) ret_type fn(__VA_ARGS__)

#define FIND_FN(fn) \
    _real_##fn = (_real_##fn##_t)dlsym(RTLD_NEXT, #fn); \
    if (_real_##fn == NULL) { \
        fprintf(stderr, "[FA] symbol not found: '%s'\n", #fn); \
        abort(); \
    }

#define ABORT_MSG \
    "FATAL: fork() was called. This is unsafe in the vmgr process. See forkaudit.c for details.\n" \
    "In Go: use pspawn.Command instead of exec.Command\n" \
    "In Rust or Swift: use posix_spawn() or write a wrapper for it\n" \
    "Aborting.\n"

DEFINE_FN(pid_t, fork, void) {
    write(STDERR_FILENO, ABORT_MSG, sizeof(ABORT_MSG) - 1);
    abort();
}

__attribute__((constructor)) void forkaudit_init(void) {
    FIND_FN(fork);
}
