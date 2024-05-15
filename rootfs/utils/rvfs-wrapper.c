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
#include <sys/resource.h>
#include <sys/syscall.h>
#include <sys/sendfile.h>
#include <sys/mman.h>
#include <sched.h>
#include <time.h>
#include <limits.h>
#include <stdbool.h>

#define DEBUG false
#define PASSTHROUGH false

// new in kernel 6.3
#define MFD_EXEC		0x0010U

// task comm keys used to select either rosetta or qemu as real binfmt_misc interpreter
static const char rvk1_data[16] = "\x03\x47\x20\xe0\xe4\x79\x3f\xbe\xae\xeb\xc7\xd6\x66\xe9\x09\x00";
static const char rvk2_data[16] = "\x20\xc2\xdc\x2b\xc5\x1f\xfe\x6b\x73\x73\x96\xee\x69\x1a\x93\x00";
static const char rvk3_data[16] = "\x41\xba\x68\x70\x7c\x66\x31\xec\x80\xe3\x2a\x30\x31\x3b\xd4\x00";

// config variables
#define RSTUB_FLAG_TSO_WORKAROUND (1 << 0)
__attribute__((section(".c0")))
const volatile uint32_t config_flags = 0;

struct elf_info {
    // interpreter (dynamic linker) path
    bool has_interp; // false = static
    char interpreter[PATH_MAX];

    // compressed by UPX?
    bool is_upx;
};

enum emu_provider {
    EMU_ROSETTA,
    EMU_QEMU,
    EMU_OVERRIDE_RUNC,
};

static int orb_perror(const char *what) {
    fprintf(stderr, "OrbStack ERROR: %s failed: %s\n", what, strerror(errno));
    fprintf(stderr, "OrbStack ERROR: Please report this bug at https://orbstack.dev/issues/bug\n");
    return 255;
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

static enum emu_provider select_emulator(int argc, char **argv, char *exe_name, struct elf_info *elf_info) {
    // milvusdb assumes AVX. QEMU 7.2+ supports AVX, but not Rosetta. https://github.com/orbstack/orbstack/issues/482
    // we don't use new QEMU due to segfaults so we can't run this anyway

    // vsce-sign also breaks in qemu so no point in switching

    // fix IBM DB2 shm issue: https://github.com/orbstack/orbstack/issues/642
    if (strncmp(exe_name, "db2", 3) == 0) {
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

    // use QEMU for UPX-packed exes
    // QEMU can handle it, Rosetta segfaults on new UPX bins and fails with "bss_size overflow" on old ones
    // these executables are really weird: only PT_LOAD, no sections
    // 3 options:
    //   1. custom loader for PT_LOAD-only bins
    //   2. append upx+runc to our wrapper exe as zip, then extract upx as memfd, and "upx -d -f -o /proc/self/fd/# <exepath>" to new memfd. (extraction = ~11 ms)
    //   3. use qemu
    // we're going with the last one for simplicity, unless it turns out to be an issue
    if (elf_info->is_upx) {
        if (DEBUG) fprintf(stderr, "selecting qemu: UPX\n");
        return EMU_QEMU;
    }

    // default
    return EMU_ROSETTA;
}

static ssize_t read_elf_size(int fd) {
    Elf64_Ehdr elf_hdr;

    // read ELF header
    if (pread(fd, &elf_hdr, sizeof(elf_hdr), 0) != sizeof(elf_hdr)) {
        return orb_perror("pread");
    }

    return elf_hdr.e_shoff + (elf_hdr.e_shnum * elf_hdr.e_shentsize);
}

static int read_elf_info(int fd, struct elf_info *out) {
    // get file size
    off_t total_size = lseek(fd, 0, SEEK_END);
    if (total_size == -1) {
        return orb_perror("lseek");
    }

    // mmap entire file
    // don't bother to unmap - we're about to exec anyway
    void *file = mmap(NULL, total_size, PROT_READ, MAP_PRIVATE, fd, 0);
    if (file == MAP_FAILED) {
        return orb_perror("mmap");
    }

    Elf64_Ehdr *ehdr = file;
    for (int i = 0; i < ehdr->e_phnum; i++) {
        Elf64_Phdr *phdr = file + (ehdr->e_phoff + i * ehdr->e_phentsize); //TODO check bounds
        if (phdr->p_type == PT_INTERP) {
            if (phdr->p_filesz > sizeof(out->interpreter)) {
                return orb_perror("interp path too long");
            }

            // copy & null terminate
            memcpy(out->interpreter, file + phdr->p_offset, phdr->p_filesz);//TODO check bounds
            out->has_interp = true;
            out->interpreter[phdr->p_filesz] = '\0';
            if (DEBUG) fprintf(stderr, "interp: %s\n", out->interpreter);
        }
    }

    // check for UPX magic "UPX!"
    if (total_size >= 256 && memmem(file, 256, "UPX!", 4) != NULL) {
        if (DEBUG) fprintf(stderr, "UPX detected\n");
        out->is_upx = true;
    }

    return 0;
}

// static arm64 build of runc is appended to our wrapper's ELF executable.
// this is OK for performance because it's mmapped and usually not touched.
// TODO: use miniz in the future if we add more appended files
static int run_override_runc(char **argv) {
    // open our own executable
    int exefd = open("/proc/self/exe", O_RDONLY|O_CLOEXEC);
    if (exefd == -1) {
        return orb_perror("open");
    }

    // create memfd
    int memfd = memfd_create("runc", MFD_EXEC);
    if (memfd == -1) {
        close(exefd);
        return orb_perror("memfd_create");
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
        close(exefd);
        close(memfd);
        return orb_perror("lseek");
    }

    // use sendfile to copy rest of file to memfd
    int remaining = total_size - elf_size;
    while (remaining > 0) {
        ssize_t ret = sendfile(memfd, exefd, NULL, remaining);
        if (ret == -1) {
            close(exefd);
            close(memfd);
            return orb_perror("sendfile");
        }
        remaining -= ret;
    }

    // start runc from memfd
    // don't need to close exefd - it's CLOEXEC
    if (syscall(SYS_execveat, memfd, "", &argv[2], environ, AT_EMPTY_PATH) != 0) {
        close(memfd);
        return orb_perror("evecveat");
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
    char **exe_argv = &argv[2];
    int exe_argc = argc - 2; /* our argv[0] + exe_path */
    char *exe_name = get_basename(exe_path);

    // get execfd
    // this errno trick works even if execfd=0
    errno = 0;
    int execfd = getauxval(AT_EXECFD);
    if (errno != 0) {
        return orb_perror("getauxval");
    }

    // no cloexec = duplicate fd leaked to process
    // however, if it's cloexec, it's marked as BINPRM_FLAGS_PATH_INACCESSIBLE
    // that means it actually tries to open the file by path instead of using fd
    // breaks systemd-executor
    if (access(exe_path, F_OK) == 0) {
        fcntl(execfd, F_SETFD, FD_CLOEXEC);
    } else {
        // explicitly clear CLOEXEC from fd, which came from parent process
        fcntl(execfd, F_SETFD, 0);
        exe_path = "";
    }

    // detect missing interpreter
    struct elf_info elf_info = {0};
    if (read_elf_info(execfd, &elf_info) == 0) {
        // check for interp if ELF parser succeeded
        if (elf_info.has_interp && access(elf_info.interpreter, F_OK) != 0) {
            // Docker container or scon/LXC machine?
            const char* env_type = access("/.dockerenv", F_OK) == 0 ? "container" : "machine";
            // missing interpreter
            fprintf(stderr, "OrbStack ERROR: Dynamic loader not found: %s\n"
                            "\n"
                            "This usually means that you're running an x86 program on an arm64 OS without multi-arch libraries.\n"
                            "To fix this, you can:\n"
                            "  1. Use an Intel (amd64) %s to run this program; or\n"
                            "  2. Install multi-arch libraries in this %s.\n"
                            "\n"
                            "This can also be caused by running a glibc executable in a musl distro (e.g. Alpine), or vice versa.\n"
                            "\n"
                            "For more details and instructions, see https://go.orbstack.dev/multiarch\n"
                            "", elf_info.interpreter, env_type, env_type);
            return 255;
        }
    }

    // select emulator
    enum emu_provider emu = PASSTHROUGH ? EMU_ROSETTA : select_emulator(argc, argv, exe_name, &elf_info);

    // ok, decision made.
    // prepare to execute.
    if (DEBUG) fprintf(stderr, "using %s for '%s'\n", emu == EMU_ROSETTA ? "rosetta" : "qemu", exe_name);

    // exec overrides instead
    if (emu == EMU_OVERRIDE_RUNC) {
        int ret = run_override_runc(argv);
        if (ret >= 0) {
            return ret;
        }

        // runc failed. continue with rosetta
        emu = EMU_ROSETTA;
    }

    char *node_argv_buf[(exe_argc + 3) + 1];
    if (emu == EMU_ROSETTA && !PASSTHROUGH) {
        // add arguments:
        // Fix Node.js programs hanging
        // "pnpm install" with large packages.json/pkgs, e.g. TypeScript, locks up with TurboFan JIT
        // webpack also freezes so it could be anything, really: https://github.com/orbstack/orbstack/issues/390
        // this is still way faster than qemu without TurboFan, and we still have Sparkplug compiler
        // --jitless works too but disables expose-wasm and requires Node 12+
        if (strcmp(exe_name, "node") == 0) {
            if (DEBUG) fprintf(stderr, "disabling Node.js TurboFan JIT\n");

            // insert argument (--no-opt)
            // then, to avoid breaking Yarn and other programs that use workers + execArgv,
            // inject a preload script to clean up process.execArgv.
            // node uses readlink, so we have to use a real /proc/.p file instead of memfd
            // can't use NODE_OPTIONS env var due to limited options. --jitless causes warning,
            // and --no-expose-wasm is not an allowed option

            node_argv_buf[0] = exe_argv[0];
            node_argv_buf[1] = "--no-opt";
            node_argv_buf[2] = "-r"; // --require
            node_argv_buf[3] = "/proc/.p";
            memcpy(&node_argv_buf[4], &exe_argv[1], (exe_argc - 1) * sizeof(char*));
            node_argv_buf[exe_argc + 3] = NULL;
            exe_argv = node_argv_buf;
        }

        // workaround for Rosetta not supporting RLIM_INFINITY stack rlimit
        // https://github.com/orbstack/orbstack/issues/573
        struct rlimit stack_lim;
        if (getrlimit(RLIMIT_STACK, &stack_lim) != 0) {
            return orb_perror("getrlimit");
        }
        if (stack_lim.rlim_cur == RLIM_INFINITY && stack_lim.rlim_max == RLIM_INFINITY) {
            // TODO: a syscall-hook shim would intercept getrlimit instead, so that application sees the correct value?
            if (DEBUG) fprintf(stderr, "setting stack rlimit to 1 GiB\n");
            // 1 GiB (virtual memory)
            stack_lim.rlim_cur = 1024 * 1024 * 1024;
            if (setrlimit(RLIMIT_STACK, &stack_lim) != 0) {
                return orb_perror("setrlimit");
            }
        }

        // workaround: macOS 14.0 (23A344) is missing TSO
        // limit rosetta processes to 1 cpu
        // TODO: check and respect existing mask if multiple cpus are in it. if only 1, then pick a new cpu (likely inherited from other rosetta).
        if (config_flags & RSTUB_FLAG_TSO_WORKAROUND) {
            // seed rng
            struct timespec ts;
            if (clock_gettime(CLOCK_MONOTONIC, &ts) != 0) {
                return orb_perror("clock_gettime");
            }
            srand(ts.tv_nsec);

            // get number of cpus
            // this is based on sched_getaffinity count, so reset it first
            cpu_set_t mask;
            CPU_ZERO(&mask);
            for (int i = 0; i < CPU_SETSIZE; i++) {
                CPU_SET(i, &mask);
            }
            if (sched_setaffinity(0, sizeof(mask), &mask) != 0) {
                return orb_perror("sched_setaffinity");
            }
            int nproc = sysconf(_SC_NPROCESSORS_ONLN);
            if (nproc == -1) {
                return orb_perror("sysconf");
            }
            if (DEBUG) fprintf(stderr, "nproc: %d\n", nproc);

            // select random cpu. current (sched_getcpu) would be nice to prevent overload, but it has problems with inheriting
            // core scheduling can't do the job.
            // TODO: syscall-hook can reset affinity on fork if we changed it, and user program has not modified it
            int cur_cpu = rand() % nproc;
            if (DEBUG) fprintf(stderr, "affine to cpu %d\n", cur_cpu);

            cpu_set_t new_mask;
            CPU_ZERO(&new_mask);
            CPU_SET(cur_cpu, &new_mask);
            if (sched_setaffinity(0, sizeof(new_mask), &new_mask) != 0) {
                // fatal: if it fails, lack of TSO will cause crashes
                return orb_perror("sched_setaffinity");
            }
        }
    }

    // resolve to absolute path, if relative
    // otherwise execveat with fd fails with ENOTDIR
    // doesn't 100% match kernel default binfmt_misc behavior, but shouldn't matter
    // can't use realpath because it resolves symlinks, breaking busybox w/o preserve-argv0
    char new_path_buf[PATH_MAX];
    // keep empty "" string
    if (strcmp(exe_path, "") != 0 && exe_path[0] != '/') {
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

    // HACK: fix dupe arg in /proc/self/cmdline for swift driver
    // rosetta preserve-argv0 is buggy so disable it for swift-driver
    // docker run -it --rm --platform linux/x86_64 swift:5.8-amazonlinux2
    // run 'swift'
    //
    // openat(AT_FDCWD, "/tmp/rosetta.11.0", O_RDWR|O_CREAT|O_EXCL, 0444) = 10
    // pwrite64(10, "/usr/bin/swift-driver\0/usr/bin/swift-driver\0--driver-mode=swift\0-Xfrontend\0-new-driver-path\0-Xfrontend\0/usr/bin/swift-driver\0", 125, 0) = 125
    //
    // swift-help breaks too: Error: The value '/usr/bin/swift-help' is invalid for '<topic>'
    // TODO: move to userspace ELF loader instead
    if (emu == EMU_ROSETTA && !PASSTHROUGH && strncmp(exe_name, "swift", 5) == 0) {
        if (DEBUG) fprintf(stderr, "swift-driver workaround\n");
        rvk_data = rvk3_data;
    }

    if (prctl(PR_SET_NAME, rvk_data, 0, 0, 0) != 0) {
        return orb_perror("prctl");
    }

    // execute by fd
    // execveat helps preserve both filename and fd
    if (syscall(SYS_execveat, execfd, exe_path, exe_argv, environ, AT_EMPTY_PATH) != 0) {
        return orb_perror("execveat");
    }

    // should never get here
    __builtin_unreachable();
}
