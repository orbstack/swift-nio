#include <unistd.h>
#include <errno.h>
#include <stdlib.h>
#include <stdbool.h>
#include <sys/uio.h>
#include <sys/syscall.h>
#include <sys/socket.h>

struct interpose_fn {
    void *new;
    void *orig;
};

#define unlikely(x) __builtin_expect(!!(x), 0)

#define DECLARE_INTERPOSE_FN(ret_type, fn) ret_type __netpose__##fn
#define INTERPOSE_FN(fn) \
    __attribute__((used, section("__DATA,__interpose"))) \
    struct interpose_fn __interposer__##fn = { .new = __netpose__##fn, .orig = fn }

#define X86_BSD_SYSCALL 0x2000000

static inline bool maybe_ejustreturn(int sys_nr, int ret, int arg1) {
#if defined(__x86_64__)
    // x86 syscall ABI: x0 = BSD syscall mask + BSD syscall number; x0 = return value
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
DECLARE_INTERPOSE_FN(ssize_t, write)(int fd, const void *buf, size_t nbyte) {
    ssize_t ret = write(fd, buf, nbyte);
    if (unlikely(maybe_ejustreturn(SYS_write, ret, fd))) {
        if (unlikely(ret > nbyte)) {
            return nbyte;
        }
    }
    return ret;
}
INTERPOSE_FN(write);

DECLARE_INTERPOSE_FN(ssize_t, writev)(int fd, const struct iovec *iovs, int iovcnt) {
    ssize_t ret = writev(fd, iovs, iovcnt);
    if (unlikely(maybe_ejustreturn(SYS_writev, ret, fd))) {
        size_t nbyte = 0;
        for (int i = 0; i < iovcnt; i++) {
            nbyte += iovs[i].iov_len;
        }
        if (unlikely(ret > nbyte)) {
            return nbyte;
        }
    }
    return ret;
}
INTERPOSE_FN(writev);

// __write_nocancel, __writev_nocancel: not used by Go or Rust code, or libc (by proxy)
// __pwrite_nocancel, __pwritev_nocancel: not used on sockets

// pwritev: not used on sockets

// send is a wrapper for __sendto. wrap it directly to reduce overhead
DECLARE_INTERPOSE_FN(ssize_t, send)(int socket, const void *buffer, size_t length, int flags) {
    ssize_t ret = sendto(socket, buffer, length, flags, NULL, 0);
    if (unlikely(maybe_ejustreturn(SYS_sendto, ret, socket))) {
        if (unlikely(ret > length)) {
            return length;
        }
    }
    return ret;
}
INTERPOSE_FN(send);

DECLARE_INTERPOSE_FN(ssize_t, sendmsg)(int socket, const struct msghdr *message, int flags) {
    ssize_t ret = sendmsg(socket, message, flags);
    if (unlikely(maybe_ejustreturn(SYS_sendmsg, ret, socket))) {
        size_t nbyte = 0;
        for (int i = 0; i < message->msg_iovlen; i++) {
            nbyte += message->msg_iov[i].iov_len;
        }
        if (unlikely(ret > nbyte)) {
            return nbyte;
        }
    }
    return ret;
}
INTERPOSE_FN(sendmsg);

// sendmsg_x: private, not used by Go or Rust code
