#include <setjmp.h>
#include <string.h>
#include <stdbool.h>
#include <signal.h>
#include <stdatomic.h>

struct filemap_thread_state {
    jmp_buf env;
    // not every thread participates
    _Atomic(bool) in_setjmp;
};

// Mach-O TLS to avoid allocations
static __thread struct filemap_thread_state thread_state;

// old handler
static struct sigaction old_action;
static _Atomic(bool) old_action_installed = false;

// must be written in C: sigsetjmp can return twice
// Rust doesn't support LLVM's returns_twice attribute
int filemap_safe_memcpy(void *dst, const void *src, size_t n) {
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
        atomic_signal_fence(memory_order_seq_cst);
        atomic_store_explicit(&state->in_setjmp, false, memory_order_relaxed);
        return -1;
    }
}

// signals are awful, but Mach exception ports aren't much better..
// async signal safety mostly still applies to in-process exception port handlers,
// and it'd have to save and forward to default ux_handler port
// this is written in C so that we can use musttail
void filemap_signal_handler(int signum, siginfo_t *info, void *uap) {
    struct filemap_thread_state *state = &thread_state;
    if (atomic_load_explicit(&state->in_setjmp, memory_order_relaxed)) {
        // we don't use SA_NODEFER (to prevent), so unmask the signal
        // otherwise, after siglongjmp, we get stuck in an infinite signal loop in the kernel (on memcpy) because the signal is still masked
        // sigsetjmp(..., 1) saves sigprocmask at the cost of an extra syscall
        sigset_t mask;
        sigemptyset(&mask);
        sigaddset(&mask, signum);
        pthread_sigmask(SIG_UNBLOCK, &mask, NULL);

        siglongjmp(state->env, -1);
        // unreachable
        return;
    }

    // forward to existing handler
    if (!atomic_load_explicit(&old_action_installed, memory_order_acquire)) {
        return;
    }

    if (old_action.sa_handler == SIG_DFL) {
        // default handler: terminate, but forward to OS to get correct exit status

        // uninstall our signal handler
        // TODO: this is wrong if signum's default sigaction != SIG_DFL. our handler won't run again. doesn't matter for SIGBUS
        struct sigaction new_action = {
            .sa_handler = SIG_DFL,
            .sa_flags = SA_RESTART,
            .sa_mask = old_action.sa_mask,
        };
        sigaction(signum, &new_action, NULL);

        // unmask the signal
        sigset_t mask;
        sigemptyset(&mask);
        sigaddset(&mask, signum);
        pthread_sigmask(SIG_UNBLOCK, &mask, NULL);

        // re-raise signal
        raise(signum);
    } else if (old_action.sa_handler == SIG_IGN) {
        // ignore: do nothing
    } else {
        // must be a tail call to prevent stack overflow in case of tiny signal stack
        // passing extra arguments to an sa_handler is ok
        __attribute__((musttail)) return old_action.sa_sigaction(signum, info, uap);
    }
}

void filemap_set_old_sigaction(struct sigaction old) {
    old_action = old;
    atomic_store_explicit(&old_action_installed, true, memory_order_release);
}
