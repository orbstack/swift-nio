#include <setjmp.h>
#include <string.h>
#include <stdbool.h>
#include <signal.h>
#include <stdatomic.h>
#include <orb_sigstack.h>

struct filemap_thread_state {
    jmp_buf env;
    // not every thread participates
    _Atomic(bool) in_setjmp;
};

// Mach-O TLS to avoid allocations
static __thread struct filemap_thread_state thread_state;

// must be written in C: sigsetjmp can return twice
// Rust doesn't support LLVM's returns_twice attribute
int orb_filemap_safe_memcpy(void *dst, const void *src, size_t n) {
    // TLS (tlv_get_addr) is slow, so reuse the pointer
    struct filemap_thread_state *state = &thread_state;

    // false = don't save sigprocmask (slow due to syscall)
    if (sigsetjmp(state->env, false) == 0) {
        // several ordering requirements:
        // - sigsetjmp must happen before in_setjmp=true
        // - both must happen before memcpy
        // - in_setjmp=false must happen after memcpy
        // this is a compiler fence to prevent reordering without inserting memory barriers
        // just need to protect against signal handler interrupting us at any point here
        atomic_signal_fence(memory_order_seq_cst);
        atomic_store_explicit(&state->in_setjmp, true, memory_order_relaxed);
        atomic_signal_fence(memory_order_seq_cst);
        memcpy(dst, src, n);
        atomic_signal_fence(memory_order_seq_cst);
        atomic_store_explicit(&state->in_setjmp, false, memory_order_relaxed);
        return 0;
    } else {
        // in_setjmp was already reset by signal handler
        return -1;
    }
}

// signals are awful, but Mach exception ports aren't much better..
// async signal safety mostly still applies to in-process exception port handlers,
// and it'd have to save and forward to default ux_handler port
signal_verdict_t orb_filemap_signal_handler(int signum, siginfo_t *info, void *uap, void *userdata) {
    struct filemap_thread_state *state = &thread_state;
    if (!atomic_load_explicit(&state->in_setjmp, memory_order_relaxed)) {
        // (not in `orb_filemap_safe_memcpy`)
        return SIGNAL_VERDICT_CONTINUE;
    }

    // clear in_setjmp here to prevent infinite loop on signal unmask if the siglongjmp target causes SIGBUS or SIGSEGV
    atomic_store_explicit(&state->in_setjmp, false, memory_order_relaxed);
    // order before unmask and siglongjmp
    atomic_signal_fence(memory_order_relaxed);

    // we don't use SA_NODEFER (to prevent), so unmask the signal
    // otherwise, after siglongjmp, we get stuck in an infinite signal loop in the kernel (on memcpy) because the signal is still masked
    // sigsetjmp(..., 1) saves sigprocmask at the cost of an extra syscall
    sigset_t mask;
    sigemptyset(&mask);
    sigaddset(&mask, signum);
    pthread_sigmask(SIG_UNBLOCK, &mask, NULL);

    siglongjmp(state->env, -1);
    // unreachable
}
