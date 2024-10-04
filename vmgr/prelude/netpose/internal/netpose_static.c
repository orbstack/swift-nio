/*
 * netpose: statically-linked interposer for socket write syscalls
 *
 * When pf blocks a packet in ip(6)_output_list, it returns -EJUSTRETURN
 * in an attempt to return success to the caller. However, ip(6)_output_list
 * just propagates the returned error from pf_af_hook, which is -EJUSTRETURN.
 * This gets propagated all the way to the userspace syscall return path, where
 * -EJUSTRETURN means to not modify userspace registers on return.
 *
 * Since a successful syscall's return value is in x0 (arm64) or RAX (x86_64),
 * callers think that the syscall succeeded with a number of bytes written equal
 * to the last value in x0/RAX. On arm64, this is the fd number, as arg1 is in x0.
 * On x86_64, this is the syscall number (+ 0x2000000 for BSD syscalls).
 *
 * If bytes written > input length, Go panics with "invalid return from write"
 * in an attempt to provide WriteAll-like semantics for TCP streams in pfd.Write.
 * Rust's write_all will also panic with a slice index out of bounds error.
 *
 * To fix this, check if the return value is the previous value of x0/RAX on the
 * respective architecture, and if so, clamp the max possible number of bytes
 * written in order to prevent panics.
 *
 * This behavior is correct because pf_af_hook returning -EJUSTRETURN is
 * supposed to pretend that the packet was successfully written, even though
 * it was dropped.
 *
 * https://github.com/golang/go/issues/61060
 */

#include <dlfcn.h>
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <sys/syscall.h>
#include <sys/uio.h>
#include <unistd.h>

#define min(a, b) ((a) < (b) ? (a) : (b))

#define unlikely(x) __builtin_expect(!!(x), 0)

// we could use dyld interposing (exp/network/netpose_dyld.c) but that's slower
// (double stubs due to vmgr -> interposer dylib -> libsystem), adds a suspicious
// dylib for reverse-engineering and AV scanning, and more fragile (dyld interposing
// is undocumented)
//
// so this is a more traditional LD_PRELOAD-style shim that uses dlsym to find
// real functions at init, and takes advantage of static linking precedence being
// higher than dynamic linking. Go's cgo_dynamic_import libc_write trampolines also
// get linked to these shims.
//
// calling out to libsystem is the same cost as a normal dyld stub, but we avoid
// an extra stub; all code in our binary calls these shims directly.
#define DEFINE_FN(ret_type, fn, ...) \
    typedef ret_type (*_real_##fn##_t)(__VA_ARGS__); \
    static _real_##fn##_t _real_##fn = NULL; \
    __attribute__((visibility("default"))) ret_type fn(__VA_ARGS__)

#define FIND_FN(fn) \
    _real_##fn = (_real_##fn##_t)dlsym(RTLD_NEXT, #fn); \
    if (_real_##fn == NULL) { \
        fprintf(stderr, "[NP] symbol not found: '%s'\n", #fn); \
        abort(); \
    }

#define X86_BSD_SYSCALL 0x2000000

static inline bool maybe_ejustreturn(int sys_nr, int ret, int arg1) {
#if defined(__x86_64__)
    // x86 syscall ABI: RAX = BSD syscall mask + BSD syscall number; RAX = return value
    return ret == X86_BSD_SYSCALL + sys_nr;
#elif defined(__arm64__)
    // arm64 syscall ABI: x0 = arg1; x0 = return value; x16 = syscall number (no BSD mask)
    return ret == arg1;
#else
#error "Unsupported architecture"
#endif
}

// not allowed to omit frame pointers on darwin arm64 :(
// https://github.com/llvm/llvm-project/blob/d4c519e7b2ac21350ec08b23eda44bf4a2d3c974/clang/lib/Driver/ToolChains/CommonArgs.cpp#L242

/*
 * cases:
 *  ret != x0/rax: no action needed
 *  ret == x0/rax: maybe EJUSTRETURN
 *    - clamp to nbyte. valid for both EJUSTRETURN and success cases
 *
 * returning success is correct, as the kernel intended to return success and drop the packet
 * silently. EHOSTUNREACH is for the other branch in pf_af_hook.
 */
DEFINE_FN(ssize_t, write, int fd, const void *buf, size_t nbyte) {
    ssize_t ret = _real_write(fd, buf, nbyte);
    if (unlikely(maybe_ejustreturn(SYS_write, ret, fd))) {
        // CSEL for better codegen in unlikely case
        return min(ret, nbyte);
    }
    return ret;
}

// for iovecs, we have to sum all iovecs to find the max possible bytes written
DEFINE_FN(ssize_t, writev, int fd, const struct iovec *iovs, int iovcnt) {
    ssize_t ret = _real_writev(fd, iovs, iovcnt);
    if (unlikely(maybe_ejustreturn(SYS_writev, ret, fd))) {
        size_t nbyte = 0;
        for (int i = 0; i < iovcnt; i++) {
            nbyte += iovs[i].iov_len;
        }
        return min(ret, nbyte);
    }
    return ret;
}

// __write_nocancel, __writev_nocancel: not used by Go or Rust code, or libc (by proxy)
// __pwrite_nocancel, __pwritev_nocancel: not used on sockets

// pwritev: not used on sockets

DEFINE_FN(ssize_t, sendto, int socket, const void *buffer, size_t length, int flags,
          const struct sockaddr *dest_addr, socklen_t dest_len) {
    ssize_t ret = _real_sendto(socket, buffer, length, flags, dest_addr, dest_len);
    if (unlikely(maybe_ejustreturn(SYS_sendto, ret, socket))) {
        return min(ret, length);
    }
    return ret;
}

// send is a wrapper for __sendto. wrap it directly to reduce overhead
DEFINE_FN(ssize_t, send, int socket, const void *buffer, size_t length, int flags) {
    ssize_t ret = _real_sendto(socket, buffer, length, flags, NULL, 0);
    if (unlikely(maybe_ejustreturn(SYS_sendto, ret, socket))) {
        return min(ret, length);
    }
    return ret;
}

DEFINE_FN(ssize_t, sendmsg, int socket, const struct msghdr *message, int flags) {
    ssize_t ret = _real_sendmsg(socket, message, flags);
    if (unlikely(maybe_ejustreturn(SYS_sendmsg, ret, socket))) {
        size_t nbyte = 0;
        for (int i = 0; i < message->msg_iovlen; i++) {
            nbyte += message->msg_iov[i].iov_len;
        }
        return min(ret, nbyte);
    }
    return ret;
}

// sendmsg_x: private, not used by Go or Rust code

__attribute__((constructor)) void netpose_init(void) {
    FIND_FN(write);
    FIND_FN(writev);
    FIND_FN(send);
    FIND_FN(sendto);
    FIND_FN(sendmsg);
}
