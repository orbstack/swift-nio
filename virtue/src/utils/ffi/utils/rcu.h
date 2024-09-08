#pragma once

#include <Mach/Mach.h>

typedef enum rcu_side {
    RCU_SIDE_LEFT = 0,
    RCU_SIDE_RIGHT = 1,
} rcu_side_t;

typedef struct rcu rcu_t;

kern_return_t _orb_rcu_create(rcu_t **out);

static inline kern_return_t rcu_create(rcu_t **out) {
    return _orb_rcu_create(out);
}

void _orb_rcu_destroy(rcu_t *rcu);

static inline void rcu_destroy(rcu_t *rcu) {
    _orb_rcu_destroy(rcu);
}

rcu_side_t _orb_rcu_begin_read(rcu_t *rcu);

static inline rcu_side_t rcu_begin_read(rcu_t *rcu) {
    return _orb_rcu_begin_read(rcu);
}

void _orb_rcu_end_read(rcu_t *rcu, rcu_side_t side);

static inline void rcu_end_read(rcu_t *rcu, rcu_side_t side) {
    _orb_rcu_end_read(rcu, side);
}

void _orb_rcu_wait_for_forgotten(rcu_t *rcu);

static inline void rcu_wait_for_forgotten(rcu_t *rcu) {
    _orb_rcu_wait_for_forgotten(rcu);
}
