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
    rcu_t *rcu;
    guarded_region_t * _Atomic head;
} global_state_t;

typedef struct local_state {
    volatile size_t scopes;
    volatile bool had_error;
} local_state_t;

static global_state_t orb_access_guard_state_global;
static __thread local_state_t orb_access_guard_state_tls;

static inline global_state_t *state_global() {
    return &orb_access_guard_state_global;
}

static inline local_state_t *state_tls() {
    return &orb_access_guard_state_tls;
}

// === Public API === //

void orb_access_guard_init_global_state() {
    MACH_CHECK_FATAL(rcu_create(&state_global()->rcu));
}

void orb_access_guard_register_guarded_region_locked(size_t base, size_t len, char *abort_msg_owned) {
    global_state_t *state = state_global();

    // Push the region to the front of the list.
    guarded_region_t *region = calloc(sizeof(guarded_region_t), 1);
    if (region == NULL) {
        FATAL("failed to allocated guarded region (OOM?)");
    }

    region->base = base;
    region->len = len;
    region->abort_msg = abort_msg_owned;
    region->next = atomic_load_explicit(&state->head, memory_order_relaxed);

    atomic_store_explicit(&state->head, region, memory_order_relaxed);

    // We're not removing anything so there's no need to wait for RCU. We do, however, enforce a
    // compiler barrier to ensure that this publish doesn't happen after we perform a potentially
    // faulting guarded memory access.
    atomic_signal_fence(memory_order_seq_cst);
}

void orb_access_guard_unregister_guarded_region_locked(size_t base) {
    global_state_t *state = state_global();

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
    free(region);
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
