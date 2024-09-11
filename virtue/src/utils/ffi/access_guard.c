#include <signal.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdlib.h>
#include <os/lock.h>
#include <Mach/Mach.h>

#include "utils/debug.h"
#include "utils/rcu.h"

// === State Definitions === //

// A region of memory guarded through this handler.
typedef struct guarded_region {
    // The next `guarded_region` in this list.
    struct guarded_region * _Atomic next;

    // The base address of the region.
    size_t base;

    // The size of this region.
    size_t len;

    // The abort message printed if we access this memory without a catch scope.
    char *abort_msg;
} guarded_region_t;

typedef struct global_state {
    // Lock taken by writers to `global_state` (i.e. `register` and `unregister`).
    os_unfair_lock lock;

    // An `rcu` controlling updates to the guarded region list.
    rcu_t *rcu;

    // The head off the guarded region linked list.
    guarded_region_t * _Atomic head;
} global_state_t;

typedef struct local_state {
    // The number of abort-absorbing scopes.
    volatile size_t scopes;

    // Whether an error has occurred since the last call to `check_for_errors`.
    volatile bool had_error;
} local_state_t;

static global_state_t orb_access_guard_state_global = {
    .lock = OS_UNFAIR_LOCK_INIT
};

static __thread local_state_t orb_access_guard_state_tls;

static inline global_state_t *state_global() {
    return &orb_access_guard_state_global;
}

static inline local_state_t *state_tls() {
    return &orb_access_guard_state_tls;
}

// === Signal Handler === //

bool orb_access_guard_signal_handler(int signum, siginfo_t *info, void *uap_raw, void *userdata) {
    global_state_t *global_state = state_global();
    local_state_t *local_state = state_tls();

    // First, let's ensure that the faulting address is protected.
    guarded_region_t *region;
    size_t fault_addr = (size_t) info->si_addr;
    {
        rcu_side_t side = rcu_begin_read(global_state->rcu);

        // This load is paired with a `release` write in `register_guarded_region`. We need this
        // ordering to ensure that the writes to the concurrently chained descriptor in
        // `register_guarded_region` is fully initialized before we traverse to it.
        region = atomic_load_explicit(&global_state->head, memory_order_acquire);

        while (region != NULL) {
            // FIXME: Technically, comparing pointers in C with different provenances is illegal but
            // I don't know how else we could solve this issue.
            if (fault_addr >= region->base && fault_addr < region->base + region->len) {
                goto addr_in_range;
            }

            region = atomic_load_explicit(&region->next, memory_order_acquire);
        }

        // The address is not protected!
        return false;

    addr_in_range:
        rcu_end_read(global_state->rcu, side);
    }

    // Now, let's see if anyone has declared interest in recovering from this error.
    if (local_state->scopes == 0) {
        // Nope! Looks like it's time to abort! This will be the case for most release code.
        goto abort;
    }

    // Time to patch `ucontext`!
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
    local_state->had_error = true;

    return true;

abort:
    printf("Encountered invalid memory operation at %zu: %s", fault_addr, region->abort_msg);
    abort();
}

// === Public API === //

void orb_access_guard_init() {
    MACH_CHECK_FATAL(rcu_create(&state_global()->rcu));
}

void orb_access_guard_register_guarded_region(size_t base, size_t len, char *abort_msg_owned) {
    global_state_t *state = state_global();
    os_unfair_lock_lock(&state->lock);

    // Push the region to the front of the list.
    guarded_region_t *region = calloc(sizeof(guarded_region_t), 1);
    if (region == NULL) {
        FATAL("failed to allocated guarded region (OOM?)");
    }

    region->base = base;
    region->len = len;
    region->abort_msg = abort_msg_owned;
    region->next = atomic_load_explicit(&state->head, memory_order_relaxed);

    atomic_store_explicit(&state->head, region, memory_order_release);

    // We're not removing anything so there's no need to wait for RCU. We do, however, enforce a
    // compiler barrier to ensure that this publish doesn't happen after we perform a potentially
    // faulting guarded memory access.
    atomic_signal_fence(memory_order_seq_cst);

    os_unfair_lock_unlock(&state->lock);
}

void orb_access_guard_unregister_guarded_region(size_t base) {
    global_state_t *state = state_global();
    os_unfair_lock_lock(&state->lock);

    // Atomically remove the entry from the list.
    guarded_region_t *region = atomic_load_explicit(&state->head, memory_order_relaxed);
    guarded_region_t * _Atomic * prev_region_next_ptr = &state->head;

    while (region != NULL) {
        if (region->base <= base && base < region->base + region->len) {
            atomic_store_explicit(
                prev_region_next_ptr,
                atomic_load_explicit(&region->next, memory_order_relaxed),
                memory_order_relaxed
            );
            break;
        }

        prev_region_next_ptr = &region->next;
        region = atomic_load_explicit(&region->next, memory_order_relaxed);
    }

    // Wait for all readers to forget about `region` before freeing it.
    rcu_wait_for_forgotten(state->rcu);
    free(region->abort_msg);
    free(region);

    os_unfair_lock_unlock(&state->lock);
}

void orb_access_guard_start_catch() {
    state_tls()->scopes += 1;
}

void orb_access_guard_end_catch() {
    state_tls()->scopes -= 1;
}

bool orb_access_guard_check_for_errors() {
    local_state_t *tls = state_tls();
    bool had_error = tls->had_error;
    tls->had_error = false;
    return had_error;
}
