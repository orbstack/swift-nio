// needed for environ
#define _GNU_SOURCE

#include <stdio.h>
#include <errno.h>
#include <stdlib.h>
#include <unistd.h>
#include <elf.h>
#include <string.h>
#include <fcntl.h>
#include <sys/prctl.h>
#include <sys/auxv.h>
#include <sys/syscall.h>
#include <sys/sendfile.h>
#include <sys/mman.h>
#include <limits.h>

#define DEBUG 0

// new in kernel 6.3
#define MFD_EXEC		0x0010U

// task comm keys used to select either rosetta or qemu as real binfmt_misc interpreter
static const char rvk1_data[16] = "\x03\x47\x20\xe0\xe4\x79\x3f\xbe\xae\xeb\xc7\xd6\x66\xe9\x09\x00";
static const char rvk2_data[16] = "\x20\xc2\xdc\x2b\xc5\x1f\xfe\x6b\x73\x73\x96\xee\x69\x1a\x93\x00";

enum emu_provider {
    EMU_ROSETTA,
    EMU_QEMU,
    EMU_OVERRIDE_RUNC,
};

static char *get_basename(char *path) {
    char *base = strrchr(path, '/');
    if (base == NULL) {
        return path;
    }
    return base + 1;
}

static enum emu_provider select_emulator(int argc, char **argv, char *exe_name) {
    // if running "apk", use qemu to avoid futex bug
    // vsce-sign also breaks in qemu so no point in switching
    if (strcmp(exe_name, "apk") == 0) {
        if (DEBUG) fprintf(stderr, "selecting qemu: exe name\n");
        return EMU_QEMU;
    }

    // if we have no access to /proc/self/exe, use qemu
    // this applies to buildkit's amd64 detection stub, which runs in a chroot
    // Rosetta requires /proc/self/exe for ioctl
    if (access("/proc/self/exe", F_OK) != 0) {
        if (DEBUG) fprintf(stderr, "selecting qemu: no access to /proc/self/exe\n");
        return EMU_QEMU;
    }

    // "runc init" fails because
    //    1. it tries to bind mount /proc/self/exe as read-only. this doesn't work b/c rosetta rvfs isn't visible to machine mount ns
    //    2. it makes a CLOEXEC memfd. doesn't work even if we unset cloexec because it fails to reopen for reading
    //       * TODO: why doesn't unsetting CLOEXEC fix it? Rosetta can read the fd but runc fails to reopen itself for reading: "you have no read access to runc" when opening /proc/self/exe
    //    3. docker wants libnetwork-setkey as a Prestart OCI hook, with exec path = /proc/<pid of dockerd>/exe.
    //       since dockerd is running under Rosetta, exe = rosetta, and it fails.
    //       we use a patched static arm64 build of runc that checks /proc/<pid>/cmdline to find real exe for rosetta.
    //       alternative would require either kernel hack or OCI pipe filter
    //
    // args = [rvfs-wrapper /usr/bin/runc runc init]
    // https://github.com/opencontainers/runc/blob/main/libcontainer/nsenter/cloned_binary.c
    //
    // maybe in the future we can solve #1 by bind mounting it somewhere into machine mount ns,
    // and #3 by launching another process that filters the OCI pipe (prestart hook)
    // that way we don't need a hacky runc override
    // OCI config is the --bundle + /config.json
    //
    // dockerd doesn't work at all under qemu b/c iptables and nftables.
    //
    // we intercept two commands:
    //    runc init
    //      * because of #1 and #2
    //    runc --root /var/run/docker/runtime-runc/moby --log /run/containerd/io.containerd.runtime.v2.task/moby/31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c/log.json --log-format json --systemd-cgroup create --bundle /run/containerd/io.containerd.runtime.v2.task/moby/31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c --pid-file /run/containerd/io.containerd.runtime.v2.task/moby/31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c/init.pid --console-socket /tmp/pty1310364487/pty.sock 31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c
    //      * because of #3
    if (argc >= 1+1+2 && strcmp(exe_name, "runc") == 0 &&
        (strcmp(argv[3], "init") == 0 || strcmp(argv[3], "--root") == 0)) {
        if (DEBUG) fprintf(stderr, "selecting runc override\n");
        return EMU_OVERRIDE_RUNC;
    }

    // default
    return EMU_ROSETTA;
}

static ssize_t read_elf_size(int fd) {
    Elf64_Ehdr elf_hdr;

    // read ELF header
    if (pread(fd, &elf_hdr, sizeof(elf_hdr), 0) != sizeof(elf_hdr)) {
        fprintf(stderr, "OrbStack ERROR: pread failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        return -1;
    }

    return elf_hdr.e_shoff + (elf_hdr.e_shnum * elf_hdr.e_shentsize);
}

// static arm64 build of runc is appended to our wrapper's ELF executable.
// this is OK for performance because it's mmapped and usually not touched.
// TODO: use miniz in the future if we add more appended files
static int run_override_runc(char **argv) {
    // open our own executable
    int exefd = open("/proc/self/exe", O_RDONLY|O_CLOEXEC);
    if (exefd == -1) {
        fprintf(stderr, "OrbStack ERROR: open failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        return 255;
    }

    // create memfd
    int memfd = memfd_create("runc", MFD_EXEC);
    if (memfd == -1) {
        fprintf(stderr, "OrbStack ERROR: memfd_create failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        close(exefd);
        return 255;
    }

    // read ELF size
    ssize_t elf_size = read_elf_size(exefd);
    if (elf_size < 0) {
        close(exefd);
        close(memfd);
        return 255;
    }

    // get total size
    off_t total_size = lseek(exefd, 0, SEEK_END);
    if (total_size == -1) {
        close(exefd);
        close(memfd);
        return 255;
    }

    // seek to end of ELF
    if (lseek(exefd, elf_size, SEEK_SET) == -1) {
        fprintf(stderr, "OrbStack ERROR: lseek failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        close(exefd);
        close(memfd);
        return 255;
    }

    // use sendfile to copy rest of file to memfd
    int remaining = total_size - elf_size;
    while (remaining > 0) {
        ssize_t ret = sendfile(memfd, exefd, NULL, remaining);
        if (ret == -1) {
            fprintf(stderr, "OrbStack ERROR: sendfile failed: %s\n", strerror(errno));
            fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
            close(exefd);
            close(memfd);
            return 255;
        }
        remaining -= ret;
    }

    // start runc from memfd
    // don't need to close exefd - it's CLOEXEC
    if (syscall(SYS_execveat, memfd, "", &argv[2], environ, AT_EMPTY_PATH) != 0) {
        fprintf(stderr, "OrbStack ERROR: execveat failed: %s\n", strerror(errno));
        fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
        close(memfd);
        return 255;
    }

    // should never get here
    fprintf(stderr, "OrbStack ERROR: execveat returned unexpectedly\n");
    fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
    close(memfd);
    return 255;
}

// our wrapper's purpose is to make a decision about which emulator to use
int main(int argc, char **argv) {
    // assume preserve-argv0 ('P'). no point in checking auxv
    char *exe_path = argv[1];
    //char *exe_argv0 = argv[2];
    char *exe_name = get_basename(exe_path);

    // select emulator
    enum emu_provider emu = select_emulator(argc, argv, exe_name);

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

    // no cloexec = duplicate fd leaked to process
    fcntl(execfd, F_SETFD, FD_CLOEXEC);

    // exec overrides instead
    if (emu == EMU_OVERRIDE_RUNC) {
        int ret = run_override_runc(argv);
        if (ret >= 0) {
            return ret;
        }

        // runc failed. continue with rosetta
        emu = EMU_ROSETTA;
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

    // resolve to absolute path, if relative
    // otherwise execveat with fd fails with ENOTDIR
    // doesn't 100% match kernel default binfmt_misc behavior, but shouldn't matter
    // can't use realpath because it resolves symlinks, breaking busybox w/o preserve-argv0
    char new_path_buf[PATH_MAX];
    if (exe_path[0] != '/') {
        char cwd[PATH_MAX];
        if (getcwd(cwd, sizeof(cwd)) == NULL) {
            // fall back to empty string, meaning that exe path becomes /dev/fd/<execfd>
            exe_path = "";
        } else {
            strcpy(new_path_buf, cwd);
            strcat(new_path_buf, "/");
            strcat(new_path_buf, exe_path);
            exe_path = new_path_buf;
        }
    }

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
