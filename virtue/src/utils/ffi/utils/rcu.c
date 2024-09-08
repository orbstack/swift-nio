#include "rcu.h"

struct rcu {
    semaphore_t sema;
    _Atomic(uint64_t) uses;
};

kern_return_t _orb_rcu_create(rcu_t **out) {
    return KERN_SUCCESS;
}

void _orb_rcu_destroy(rcu_t* rcu) {

}

void _orb_rcu_begin_read(rcu_t* rcu) {

}

void _orb_rcu_end_read(rcu_t *rcu) {

}

void _orb_rcu_wait_for_forgotten(rcu_t *rcu) {

}

