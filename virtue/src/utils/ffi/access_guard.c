#include <signal.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdlib.h>
#include <os/lock.h>
#include <mach/mach.h>

#include <orb_sigstack.h>
#include "utils/aprintf.h"
#include "utils/debug.h"

typedef __int128 orb_u128_t;

#define GUARDED_REGION_MAX (4)

// === State Definitions === //

// A region of memory guarded through this handler.
struct guarded_region {
    // An atomic tracking...
    //
    // - Low 64 bits: the base address of the region
    // - High 64 bits: the size of the region
    //
    _Atomic(orb_u128_t) state;

    // The abort message printed if we access this memory without a catch scope. These strings are
    // never deallocated.
    _Atomic(char *) abort_msg;
};

struct global_state {
    // Lock taken by writers to `global_state` (i.e. `register` and `unregister`).
    os_unfair_lock lock;

    // The buffer of guarded regions
    struct guarded_region regions[GUARDED_REGION_MAX];
};

struct fault_state {
    // This value is `0` if no errors have occurred since the last check.
    size_t region_base;
    size_t fault_addr;
};

struct local_state {
    // The number of abort-absorbing scopes.
    volatile size_t scopes;

    // The first fault to have handled by the signal handler.
    struct fault_state first_fault;
};

static struct global_state state_global = {
    .lock = OS_UNFAIR_LOCK_INIT
};

static __thread struct local_state state_tls;

// === Signal Handler === //

signal_verdict_t orb_access_guard_signal_handler(int signum, siginfo_t *info, void *uap_raw, void *userdata) {
    struct local_state *local_state = &state_tls;

    // First, let's ensure that the faulting address is protected.
    size_t region_base;
    size_t region_len;
    char *region_abort_msg;
    size_t fault_addr = (size_t) info->si_addr;
    bool addr_in_range = false;

    for (int i = 0; i < GUARDED_REGION_MAX; i++) {
        struct guarded_region *region = &state_global.regions[i];

        orb_u128_t state = atomic_load_explicit(&region->state, memory_order_relaxed);
        region_base = (uint64_t)state;
        region_len = (uint64_t)(state >> 64);

        region_abort_msg = atomic_load_explicit(&region->abort_msg, memory_order_relaxed);

        // FIXME: Technically, comparing pointers in C with different provenances is illegal but
        // I don't know how else we could solve this issue.
        if (fault_addr >= region_base && fault_addr < region_base + region_len) {
            addr_in_range = true;
        }
    }

    if (!addr_in_range) {
        // The address is not protected!
        return SIGNAL_VERDICT_CONTINUE;
    }

    // Now, let's see if anyone has declared interest in recovering from this error.
    if (local_state->scopes == 0) {
        // Nope! Looks like it's time to abort! This will be the case for most release code.
        goto abort;
    }

    // Let's try to patch `ucontext`!
    //
    // Rust does not seem to like pointers to `ucontext_t` because it thinks they contain `u128`s
    // which have super iffy ABIs so we just keep it as a `void*` and downcast it here.
    ucontext_t *uap = uap_raw;
    mcontext_t mcx = uap->uc_mcontext;  // `mcontext_t` is a pointer to the actual struct

#if defined (__arm64__)
    // See `/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/include/arm/_mcontext.h`
    // and `/Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/SDKs/MacOSX.sdk/usr/include/mach/arm/_structs.h`
    // ...for the definitions we're using.

    // Patching the return value of these operations could be quite tricky so we avoid it and leave
    // the register in some undefined state. This is debug logic so it only has to get us through to
    // the error reporter.
    arm_thread_state64_t *ss = &mcx->__ss;
    ss->__pc += 4;
#else
    // Catching is unsupported on this architecture. It's a debug operation so we can just abort.
    goto abort;
#endif

    // Flag the error so userland can process it.
    struct fault_state fault = local_state->first_fault;
    if (fault.region_base == 0) {
        fault.region_base = region_base;
        fault.fault_addr = fault_addr;
        local_state->first_fault = fault;
    }

    return SIGNAL_VERDICT_HANDLED;

abort:
    aprintf(
        "detected invalid memory operation in protected region at relative address 0x%zu (region starts at 0x%zu): %s\n",
        fault_addr - region_base,
        region_base,
        region_abort_msg != NULL ? region_abort_msg : "<no abort message supplied>"
    );

    // Let the default SIGBUS handler dump the process. We intentionally skip over Go's default handler.
    //
    // Go's default handlers seem to just dump the state of all its goroutines under the presumption
    // that this could help debug an issue triggered by unsafe cgo usage but, in this case, the bug
    // is purely `libkrun`'s fault so let's not spam the logs with unnecessary details.
    return SIGNAL_VERDICT_FORCE_DEFAULT;
}

// === Public API === //

void orb_access_guard_register_guarded_region(size_t base, size_t len, char *abort_msg) {
    os_unfair_lock_lock(&state_global.lock);

    // Find a slot in the list.
    struct guarded_region *region;
    bool found_free = false;
    for (int i = 0; i < GUARDED_REGION_MAX; i++) {
        region = &state_global.regions[i];
        if (atomic_load_explicit(&region->state, memory_order_relaxed) == 0) {
            found_free = true;
        }
    }

    if (!found_free) {
        FATAL("Allocated too many guarded regions!");
    }

    // Initialize its state. It's okay if racing fault handlers see the wrong abort message since
    // it's only diagnostic for end users.
    orb_u128_t re_state = ((orb_u128_t)base) + (((orb_u128_t)len) << 64);
    atomic_store_explicit(&region->state, re_state, memory_order_relaxed);
    atomic_store_explicit(&region->abort_msg, abort_msg, memory_order_relaxed);

    // We enforce a compiler barrier to ensure that this publish doesn't happen after we perform a
    // potentially faulting guarded memory access.
    atomic_signal_fence(memory_order_seq_cst);

    os_unfair_lock_unlock(&state_global.lock);
}

void orb_access_guard_unregister_guarded_region(size_t base) {
    os_unfair_lock_lock(&state_global.lock);

    for (int i = 0; i < GUARDED_REGION_MAX; i++) {
        struct guarded_region *region = &state_global.regions[i];

        orb_u128_t state = atomic_load_explicit(&region->state, memory_order_relaxed);
        size_t region_base = (uint64_t)state;
        size_t region_len = (uint64_t)(state >> 64);

        if (region_base <= base && base < region_base + region_len) {
            atomic_store_explicit(&region->state, 0, memory_order_relaxed);
            atomic_store_explicit(&region->abort_msg, NULL, memory_order_relaxed);
        }
    }

    // We enforce a compiler barrier to ensure that this publish doesn't happen after we perform a
    // potentially faulting *un*guarded memory access.
    atomic_signal_fence(memory_order_seq_cst);

    os_unfair_lock_unlock(&state_global.lock);
}

void orb_access_guard_start_catch() {
    state_tls.scopes += 1;
}

void orb_access_guard_end_catch() {
    state_tls.scopes -= 1;
}

struct fault_state orb_access_guard_check_for_errors() {
    struct local_state *tls = &state_tls;
    struct fault_state state = tls->first_fault;

    if (state.region_base != 0) {
        tls->first_fault = (struct fault_state){0, 0};
    }

    return state;
}
