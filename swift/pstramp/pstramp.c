/*
 * pstramp: posix_spawn trampoline
 *
 * posix_spawn(2) is better than fork+execve (faster and cloexec-safe). In fact, vmgr can *only* use
 * posix_spawn because it's infeasible to synchronize cloexec safety between Go/Rust/Swift/C on
 * macOS, and it has a forkaudit module that stubs fork() in order to enforce this. Unfortunately,
 * it's missing some features for calls that we may want to make between fork() and execve(). So, in
 * order to use posix_spawn in all cases, we need to posix_spawn a trampoline that makes those calls
 * and then execve()s the real executable.
 *
 * Currently supported actions:
 * - setctty
 *
 * For security, this executable is signed with launch constraints that require the caller to be
 * signed by the OrbStack team ID + vmgr/scli signing ID. This prevents this trampoline from being
 * abused by other programs (in combination with responsibility_spawnattrs_setdisclaim) to gain our
 * TCC identity by having pstramp become the responsible process.
 */

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include <stdbool.h>
#include <spawn.h>

extern int responsibility_spawnattrs_setdisclaim(posix_spawnattr_t *attrs, int disclaim) __attribute__((weak_import));
extern char **environ;

// Usage: ./pstramp [-setctty fd#] -- <exe> <argv...> (including argv0)
int main(int argc, const char *argv[]) {
    const char *exe = NULL;
    const char **exe_argv = NULL;
    bool disclaim = false;
    for (int i = 0; i < argc; i++) {
        if (strcmp(argv[i], "-setctty") == 0) {
            // setctty
            int tty_fd = atoi(argv[++i]);
            int ret = ioctl(tty_fd, TIOCSCTTY, 0);
            if (ret == -1) {
                fprintf(stderr, "ioctl(TIOCSCTTY) failed: %s\n", strerror(errno));
                return 254;
            }
        } else if (strcmp(argv[i], "-disclaim") == 0) {
            // disclaim TCC responsibility
            disclaim = true;
        } else if (strcmp(argv[i], "--") == 0) {
            // end of options
            exe = argv[++i];
            exe_argv = &argv[i + 1];
            break;
        }
    }

    // use posix_spawn+POSIX_SPAWN_SETEXEC instead of execve, for features like disclaiming
    posix_spawnattr_t attr;
    int ret = posix_spawnattr_init(&attr);
    if (ret != 0) {
        fprintf(stderr, "posix_spawnattr_init() failed: %s\n", strerror(ret));
        return 254;
    }
    ret = posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETEXEC);
    if (ret != 0) {
        fprintf(stderr, "posix_spawnattr_setflags() failed: %s\n", strerror(ret));
        return 254;
    }
    if (disclaim && responsibility_spawnattrs_setdisclaim) {
        responsibility_spawnattrs_setdisclaim(&attr, 1);
    }

    // exec the real executable
    // (what an abomination: "char *const *" = non-const ptr to const ptr to non-const char)
    ret = posix_spawn(NULL, exe, NULL, &attr, (char *const *)exe_argv, environ);
    if (ret == -1) {
        fprintf(stderr, "posix_spawn(%s) failed: %s\n", exe, strerror(errno));
        return 254;
    }

    __builtin_unreachable();
}
