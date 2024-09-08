#include <stdbool.h>
#include <stdlib.h>

#include "utils/debug.h"

// === Public API === //

size_t orb_access_guard_register_guarded_region(void *base, size_t len, char *abort_msg_owned) {
    TODO();
}

void orb_access_guard_unregister_guarded_region(size_t handle) {
    TODO();
}

void orb_access_guard_start_catch() {
    TODO();
}

void orb_access_guard_end_catch() {
    TODO();
}

bool orb_access_guard_check_for_errors() {
    TODO();
}
