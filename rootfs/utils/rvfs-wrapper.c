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
#include <stdbool.h>

#define DEBUG false
#define PASSTHROUGH false

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

static char *orb_perror(const char *what) {
    fprintf(stderr, "OrbStack ERROR: %s failed: %s\n", what, strerror(errno));
    fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
    return NULL;
}

static char *get_basename(char *path) {
    char *base = strrchr(path, '/');
    if (base == NULL) {
        return path;
    }
    return base + 1;
}

static bool argv_contains(char **argv, char *what) {
    for (int i = 0; argv[i] != NULL; i++) {
        if (strcmp(argv[i], what) == 0) {
            return true;
        }
    }
    return false;
}

static enum emu_provider select_emulator(int argc, char **argv, char *exe_name) {
    // if running "apk", use qemu to avoid futex bug
    if (strcmp(exe_name, "apk") == 0) {
        if (DEBUG) fprintf(stderr, "selecting qemu: exe name\n");
        return EMU_QEMU;
    }

    // vsce-sign also breaks in qemu so no point in switching

    // fix "build-script-build" getting stuck on futex during cargo build
    // ex: /build/vinit/target/release/build/bzip2-sys-7a5f3f458c874dc9/build-script-build
    if (strcmp(exe_name, "build-script-build") == 0) {
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
    // we intercept 3 commands:
    //    runc init
    //      * because of #1 and #2
    //    runc --root /var/run/docker/runtime-runc/moby --log /run/containerd/io.containerd.runtime.v2.task/moby/31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c/log.json --log-format json --systemd-cgroup create --bundle /run/containerd/io.containerd.runtime.v2.task/moby/31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c --pid-file /run/containerd/io.containerd.runtime.v2.task/moby/31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c/init.pid --console-socket /tmp/pty1310364487/pty.sock 31fefad1a9ca5dc6f3f6236e0806377d934fc80b638f0c9026b44ac0ad9fcd6c
    //      * because of #3
    //   runc --log /var/lib/docker/buildkit/executor/runc-log.json --log-format json run --bundle /var/lib/docker/buildkit/executor/m66aufx3xv7s2yq91dzxtemvm m66aufx3xv7s2yq91dzxtemvm
    //      * because of #3, when building docker image
    if (argc >= 1+1+2 && strcmp(exe_name, "runc") == 0 &&
        (strcmp(argv[3], "init") == 0 || argv_contains(argv, "--bundle"))) {
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
        orb_perror("pread");
        return -1;
    }

    return elf_hdr.e_shoff + (elf_hdr.e_shnum * elf_hdr.e_shentsize);
}

static int read_interp(int fd, char interp_buf[PATH_MAX], bool *pt_interp_after_load) {
    Elf64_Ehdr ehdr;
    if (pread(fd, &ehdr, sizeof(ehdr), 0) != sizeof(ehdr)) {
        perror("pread ehdr");
        return -1;
    }

    Elf64_Phdr phdr;
    bool seen_pt_load = false;
    for (int i = 0; i < ehdr.e_phnum; i++) {
        off_t offset = ehdr.e_phoff + i * ehdr.e_phentsize;
        if (pread(fd, &phdr, sizeof(phdr), offset) != sizeof(phdr)) {
            perror("pread phdr");
            return -1;
        }

        if (phdr.p_type == PT_INTERP) {
            if (phdr.p_filesz > PATH_MAX) {
                if (DEBUG) fprintf(stderr, "interp path too long\n");
                return -1;
            }

            if (pread(fd, interp_buf, phdr.p_filesz, phdr.p_offset) != phdr.p_filesz) {
                perror("pread interp");
                return -1;
            }

            if (DEBUG) fprintf(stderr, "interp: %s\n", interp_buf);
            // null terminate
            interp_buf[phdr.p_filesz] = '\0';
            // set flag for PT_INTERP after LOAD
            if (seen_pt_load) {
                *pt_interp_after_load = true;
            }
            return 0;
        } else if (phdr.p_type == PT_LOAD) {
            seen_pt_load = true;
        }
    }

    // not found - could be static
    return -1;
}

// static arm64 build of runc is appended to our wrapper's ELF executable.
// this is OK for performance because it's mmapped and usually not touched.
// TODO: use miniz in the future if we add more appended files
static int run_override_runc(char **argv) {
    // open our own executable
    int exefd = open("/proc/self/exe", O_RDONLY|O_CLOEXEC);
    if (exefd == -1) {
        orb_perror("open");
        return 255;
    }

    // create memfd
    int memfd = memfd_create("runc", MFD_EXEC);
    if (memfd == -1) {
        orb_perror("memfd_create");
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
        orb_perror("lseek");
        close(exefd);
        close(memfd);
        return 255;
    }

    // use sendfile to copy rest of file to memfd
    int remaining = total_size - elf_size;
    while (remaining > 0) {
        ssize_t ret = sendfile(memfd, exefd, NULL, remaining);
        if (ret == -1) {
            orb_perror("sendfile");
            close(exefd);
            close(memfd);
            return 255;
        }
        remaining -= ret;
    }

    // start runc from memfd
    // don't need to close exefd - it's CLOEXEC
    if (syscall(SYS_execveat, memfd, "", &argv[2], environ, AT_EMPTY_PATH) != 0) {
        orb_perror("evecveat");
        close(memfd);
        return 255;
    }

    // should never get here
    __builtin_unreachable();
}

// our wrapper's purpose is to make a decision about which emulator to use
int main(int argc, char **argv) {
    if (argc == 1) {
        fprintf(stderr, "Please be mindful of the end-user license agreement.\nhttps://docs.orbstack.dev/legal/terms\nCopyright 2023 Orbital Labs, LLC. All rights reserved.\n\nHaving fun? Say hi at secret@orbstack.dev :)\n");
        return 0;
    }

    // assume preserve-argv0 ('P'). no point in checking auxv
    char *exe_path = argv[1];
    char *exe_argv0 = argv[2];
    char *exe_name = get_basename(exe_path);

    // select emulator
    enum emu_provider emu = PASSTHROUGH ? EMU_ROSETTA : select_emulator(argc, argv, exe_name);

    // ok, decision made.
    // prepare to execute.
    if (DEBUG) fprintf(stderr, "using %s for '%s'\n", emu == EMU_ROSETTA ? "rosetta" : "qemu", exe_name);

    // if using Rosetta and running "node" or "nvim", set UV_USE_IO_URING=0 if not already set in environ
    // this is a crude way to detect libuv and avoid 100% CPU on io_uring: https://github.com/orbstack/orbstack/issues/377
    if (!PASSTHROUGH &&
            emu == EMU_ROSETTA &&
            (strcmp(exe_name, "node") == 0 || strcmp(exe_name, "nvim") == 0) &&
            getenv("UV_USE_IO_URING") == NULL) {
        if (DEBUG) fprintf(stderr, "setting UV_USE_IO_URING=0\n");
        setenv("UV_USE_IO_URING", "0", 0);
    }

    // get execfd
    int execfd = getauxval(AT_EXECFD);
    if (execfd == 0) {
        orb_perror("getauxval");
        return 255;
    }

    // no cloexec = duplicate fd leaked to process
    // TODO: what if this is BINPRM_FLAGS_PATH_INACCESSIBLE?
    fcntl(execfd, F_SETFD, FD_CLOEXEC);

    // detect missing interpreter
    char interpreter[PATH_MAX];
    bool pt_interp_after_load = false;
    if (read_interp(execfd, interpreter, &pt_interp_after_load) == 0) {
        // check for interp if ELF parser succeeded
        if (access(interpreter, F_OK) != 0) {
            // missing interpreter
            fprintf(stderr, "OrbStack ERROR: Dynamic loader not found: %s\n"
                            "\n"
                            "This usually means that you're running an x86 program on an arm64 OS without multi-arch libraries.\n"
                            "To fix this, you can:\n"
                            "  1. Run the program in an Intel (amd64) container or machine instead.\n"
                            "  2. Install multi-arch libraries in this container or machine.\n"
                            "\n"
                            "For more details and instructions, see https://docs.orbstack.dev/readme-link/multiarch\n"
                            "", interpreter);
            return 255;
        }
    }

    // exec overrides instead
    if (emu == EMU_OVERRIDE_RUNC) {
        int ret = run_override_runc(argv);
        if (ret >= 0) {
            return ret;
        }

        // runc failed. continue with rosetta
        emu = EMU_ROSETTA;
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

    // set task comm key
    // this indicates to kernel (binfmt_misc) which handler to use
    // do this last to minimize time window with garbage in comm
    const char *rvk_data = emu == EMU_ROSETTA ? rvk1_data : rvk2_data;
    if (prctl(PR_SET_NAME, rvk_data, 0, 0, 0) != 0) {
        orb_perror("prctl");
        return 255;
    }

    // patchelf workaround: Rosetta segfaults if PT_INTERP is after PT_LOAD
    // as a workaround, we invoke the dynamic linker directly instead
    if (emu == EMU_ROSETTA && pt_interp_after_load) {
        // create new argv: [exe_argv0, exe_path, ...&argv[3]]
        char *new_argv[2 + (argc - 3) + 1];
        new_argv[0] = exe_argv0;
        new_argv[1] = exe_path;
        memcpy(&new_argv[2], &argv[3], (argc - 3) * sizeof(char*));
        new_argv[2 + (argc - 3)] = NULL;

        if (execve(interpreter, new_argv, environ) != 0) {
            orb_perror("execve");
            return 255;
        }
    }

    // execute by fd
    // execveat helps preserve both filename and fd
    if (syscall(SYS_execveat, execfd, exe_path, &argv[2] /* &exe_argv0 */, environ, AT_EMPTY_PATH) != 0) {
        orb_perror("execveat");
        return 255;
    }

    // should never get here
    __builtin_unreachable();
}
