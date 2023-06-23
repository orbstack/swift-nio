// needed for environ
#define _GNU_SOURCE

#include <stdio.h>
#include <errno.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/prctl.h>
#include <sys/auxv.h>
#include <elf.h>
#include <string.h>
#include <sys/syscall.h>
#include <fcntl.h>

#define DEBUG 0

// task comm keys used to select either rosetta or qemu as real binfmt_misc interpreter
static const char rvk1_data[16] = "\x03\x47\x20\xe0\xe4\x79\x3f\xbe\xae\xeb\xc7\xd6\x66\xe9\x09\x00";
static const char rvk2_data[16] = "\x20\xc2\xdc\x2b\xc5\x1f\xfe\x6b\x73\x73\x96\xee\x69\x1a\x93\x00";

enum emu_provider {
    EMU_ROSETTA,
    EMU_QEMU,
};

static char *get_basename(char *path) {
    char *base = strrchr(path, '/');
    if (base == NULL) {
        return path;
    }
    return base + 1;
}

// our wrapper's purpose is to make a decision about which emulator to use
int main(int argc, char **argv) {
    // default to rosetta
    enum emu_provider emu = EMU_ROSETTA;

    // assume preserve-argv0 ('P'). no point in checking auxv
    char *exe_path = argv[1];
    //char *exe_argv0 = argv[2];
    char *exe_name = get_basename(exe_path);

    // if running "apk", use qemu to avoid futex bug
    // vsce-sign also breaks in qemu so no point in switching
    if (strcmp(exe_name, "apk") == 0) {
        if (DEBUG) fprintf(stderr, "selecting qemu: exe name\n");
        emu = EMU_QEMU;
    }

    // if we have no access to /proc/self/exe, use qemu
    // this applies to buildkit's amd64 detection stub, which runs in a chroot
    // Rosetta requires /proc/self/exe for ioctl
    if (access("/proc/self/exe", F_OK) != 0) {
        if (DEBUG) fprintf(stderr, "selecting qemu: no access to /proc/self/exe\n");
        emu = EMU_QEMU;
    }

    // ok, decision made.
    // prepare to execute.
    if (DEBUG) fprintf(stderr, "using %s for '%s'\n", emu == EMU_ROSETTA ? "rosetta" : "qemu", exe_name);

    // if using Rosetta and running "node" or "nvim", set UV_USE_IO_URING=0 if not already set in environ
    // this is a crude way to detect libuv and avoid 100% CPU on io_uring: https://github.com/orbstack/orbstack/issues/377
    if (emu == EMU_ROSETTA &&
            (strcmp(exe_name, "node") == 0 || strcmp(exe_name, "nvim") == 0) &&
            getenv("UV_USE_IO_URING") == NULL) {
        if (DEBUG) fprintf(stderr, "setting UV_USE_IO_URING=0\n");
        setenv("UV_USE_IO_URING", "0", 0);
    }

    // get execfd
    int execfd = getauxval(AT_EXECFD);
    if (execfd == 0) {
        fprintf(stderr, "OrbStack ERROR: getauxval failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        return 255;
    }

    // set task comm key
    // this indicates to kernel (binfmt_misc) which handler to use
    // do this last to minimize time window with garbage in comm
    const char *rvk_data = emu == EMU_ROSETTA ? rvk1_data : rvk2_data;
    if (prctl(PR_SET_NAME, rvk_data, 0, 0, 0) != 0) {
        fprintf(stderr, "OrbStack ERROR: prctl failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        return 255;
    }

    // no cloexec = duplicate fd leaked to process
    fcntl(execfd, F_SETFD, FD_CLOEXEC);

    // execute by fd
    // execveat helps preserve both filename and fd
    if (syscall(SYS_execveat, execfd, exe_path, &argv[2], environ, AT_EMPTY_PATH) != 0) {
        fprintf(stderr, "OrbStack ERROR: execveat failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        return 255;
    }

    // should never get here
    fprintf(stderr, "OrbStack ERROR: execveat returned unexpectedly\n");
    fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
    return 255;
}
