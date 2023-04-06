//
//  GoVZF.h
//  GoVZF
//
//  Created by Danny Lin on 3/3/23.
//

#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>

struct GovzfResultCreate {
    void* ptr;
    char* err;
    bool rosetta_canceled;
};

struct GovzfResultErr {
    char* err;
};

struct GovzfResultIntErr {
    int64_t value;
    char* err;
};

#ifndef CGO
void govzf_event_Machine_deinit(uintptr_t vmHandle);
void govzf_event_Machine_onStateChange(uintptr_t vmHandle, int state);

void swext_proxy_cb_changed(void);
#endif