#pragma once

#include <Mach/Mach.h>

typedef struct rcu rcu_t;

kern_return_t _orb_rcu_create(rcu_t **out);

static inline kern_return_t rcu_create(rcu_t **out) {
    return _orb_rcu_create(out);
}

void _orb_rcu_destroy(rcu_t *rcu);

static inline void rcu_destroy(rcu_t *rcu) {
    _orb_rcu_destroy(rcu);
}

void _orb_rcu_begin_read(rcu_t *rcu);

static inline void rcu_begin_read(rcu_t *rcu) {
    _orb_rcu_begin_read(rcu);
}

void _orb_rcu_end_read(rcu_t *rcu);

static inline void rcu_end_read(rcu_t *rcu) {
    _orb_rcu_end_read(rcu);
}

void _orb_rcu_wait_for_forgotten(rcu_t *rcu);

static inline void rcu_wait_for_forgotten(rcu_t *rcu) {
    _orb_rcu_wait_for_forgotten(rcu);
}
