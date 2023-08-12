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

struct elf_info {
    // interpreter (dynamic linker) path
    bool has_interp; // false = static
    char interpreter[PATH_MAX];

    // links against libuv?
    bool needs_libuv;

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
    if (strcmp(exe_name, "milvus") == 0) {
        if (DEBUG) fprintf(stderr, "selecting qemu: exe name\n");
        return EMU_QEMU;
    }

    // vsce-sign also breaks in qemu so no point in switching

    // fix "build-script-build" getting stuck on futex during cargo build
    // futex(0xffff86e5bfb4, FUTEX_WAIT_PRIVATE, 1, NULL
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
    bool seen_pt_load = false;
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
        } else if (phdr->p_type == PT_DYNAMIC) {
            // find string table (STRTAB)
            char *strtab = NULL;
            for (int j = 0; j < phdr->p_filesz / sizeof(Elf64_Dyn); j++) {
                Elf64_Dyn *dyn = file + (phdr->p_offset + j * sizeof(Elf64_Dyn)); //TODO check bounds
                if (dyn->d_tag == DT_STRTAB) {
                    // dyn->d_un.d_ptr is the loaded virtual address, not file offset
                    // find PT_LOAD segment to translate it
                    Elf64_Phdr *load_phdr = NULL;
                    for (int k = 0; k < ehdr->e_phnum; k++) {
                        Elf64_Phdr *tmp_phdr = file + (ehdr->e_phoff + k * ehdr->e_phentsize);
                        if (tmp_phdr->p_type == PT_LOAD && dyn->d_un.d_ptr >= tmp_phdr->p_vaddr && dyn->d_un.d_ptr < (tmp_phdr->p_vaddr + tmp_phdr->p_memsz)) {
                            load_phdr = tmp_phdr;
                            break;
                        }
                    }
                    if (load_phdr == NULL) {
                        return orb_perror("missing LOAD segment for STRTAB");
                    }

                    strtab = file + load_phdr->p_offset + (dyn->d_un.d_ptr - load_phdr->p_vaddr);
                    break;
                }
            }
            if (strtab == NULL) {
                return orb_perror("missing DT_STRTAB");
            }

            // check DT_NEEDED tags
            for (int j = 0; j < phdr->p_filesz / sizeof(Elf64_Dyn); j++) {
                Elf64_Dyn *dyn = file + (phdr->p_offset + j * sizeof(Elf64_Dyn)); //TODO check bounds
                if (dyn->d_tag == DT_NEEDED) {
                    char libname[PATH_MAX];
                    strncpy(libname, strtab + dyn->d_un.d_val, sizeof(libname) - 1);
                    if (DEBUG) fprintf(stderr, "needed: %s\n", libname);

                    // is it libuv?
                    if (strstr(libname, "libuv.so") != NULL) {
                        if (DEBUG) fprintf(stderr, "needs libuv\n");
                        out->needs_libuv = true;
                    }
                }
            }
        } else if (phdr->p_type == PT_LOAD) {
            seen_pt_load = true;
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
    // TODO: what if this is BINPRM_FLAGS_PATH_INACCESSIBLE?
    fcntl(execfd, F_SETFD, FD_CLOEXEC);

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
                            "This can also be caused by running a glibc executable in a musl distro, or vice versa.\n"
                            "\n"
                            "For more details and instructions, see https://docs.orbstack.dev/readme-link/multiarch\n"
                            "", elf_info.interpreter, env_type, env_type);
            return 255;
        }

        // if using Rosetta and running "node" or "nvim", set UV_USE_IO_URING=0 if not already set in environ
        // we check DT_NEEDED libraries but node.js might be statically linked
        // this is a crude way to detect libuv and avoid 100% CPU on io_uring: https://github.com/orbstack/orbstack/issues/377
        if (!PASSTHROUGH && (elf_info.needs_libuv || !strcmp(exe_name, "node"))) {
            if (DEBUG) fprintf(stderr, "setting UV_USE_IO_URING=0\n");
            setenv("UV_USE_IO_URING", "0", /*overwrite*/ 0);
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

    // add arguments:
    // Fix Node.js programs hanging
    // "pnpm install" with large packages.json/pkgs, e.g. TypeScript, locks up with TurboFan JIT
    // webpack also freezes so it could be anything, really: https://github.com/orbstack/orbstack/issues/390
    // this is still way faster than qemu without TurboFan, and we still have Sparkplug compiler
    // --jitless works too but disables expose-wasm and requires Node 12+
    char *node_argv_buf[(exe_argc + 1) + 1];
    if (!PASSTHROUGH && strcmp(exe_name, "node") == 0) {
        if (DEBUG) fprintf(stderr, "disabling Node.js TurboFan JIT\n");

        // need to insert an argument
        node_argv_buf[0] = exe_argv[0];
        node_argv_buf[1] = "--no-opt";
        memcpy(&node_argv_buf[2], &exe_argv[1], (exe_argc - 1) * sizeof(char*));
        node_argv_buf[exe_argc + 1] = NULL;
        exe_argv = node_argv_buf;
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
