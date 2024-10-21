#include <stdatomic.h>
#include <stdlib.h>

#include "debug.h"
#include "rcu.h"

// TODO: Use proper orderings.

struct rcu {
    semaphore_t sema;
    _Atomic(uint8_t) rcu_side;
    _Atomic(uint64_t) readers[2];
};

kern_return_t _orb_rcu_create(rcu_t **rcu) {
    kern_return_t err;

    // Allocate semaphore
    semaphore_t sema;
    err = semaphore_create(current_task(), &sema, SYNC_POLICY_FIFO, 0);
    if (err != KERN_SUCCESS) {
        goto fail;
    }

    // Allocate RCU structure
    *rcu = calloc(sizeof(rcu_t), 1);
    if (*rcu == NULL) {
        err = KERN_MEMORY_ERROR;  // :shrug:
        goto fail;
    }

    // Initialize RCU structure
    (*rcu)->sema = sema;

    return KERN_SUCCESS;

fail:
    *rcu = NULL;
    return err;
}

void _orb_rcu_destroy(rcu_t* rcu) {
    MACH_CHECK_FATAL(semaphore_destroy(rcu->sema, current_task()));
    free(rcu);
}

rcu_side_t _orb_rcu_begin_read(rcu_t* rcu) {
    rcu_side_t side = atomic_load(&rcu->rcu_side);
    atomic_fetch_add(&rcu->readers[side], 1);
    return side;
}

void _orb_rcu_end_read(rcu_t *rcu, rcu_side_t side) {
    // Decrement the reader counter on the side from which we originally borrowed.
    uint64_t reader_count = atomic_fetch_sub(&rcu->readers[side], 1);

    // If the side changed from when we began our read, we know that the writer thread is blocking
    // or is about to block on `semaphore_wait`. If we were the last reader on this side, we know
    // that all readers are now operating on the new structure and can therefore wakeup the writer
    // to tell it that the old structure has been forgotten.
    if (atomic_load(&rcu->rcu_side) != side && reader_count == 1) {
        MACH_CHECK_FATAL(semaphore_signal(rcu->sema));
    }
}

void _orb_rcu_wait_for_forgotten(rcu_t *rcu) {
    // Tell new readers to use the other "side" of the RCU reader counter.
    rcu_side_t old_side = atomic_load(&rcu->rcu_side);
    atomic_store(&rcu->rcu_side, 1 - old_side);

    // Check whether any other thread is actively accessing our structure.
    uint64_t readers = atomic_load(&rcu->readers[old_side]);

    if (readers == 0) {
        // If there aren't any active users, we know that all readers have properly forgotten
        // about the structure. Let's fast-path out!
        return;
    }

    // Otherwise, wait for the other side to finish their operations.
    MACH_CHECK_FATAL(semaphore_wait(rcu->sema));
}
