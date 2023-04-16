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

struct krpc_header {
    uint32_t len;
    uint32_t typ;
} __attribute__((packed));

struct krpc_notifyproxy_inject  {
    uint64_t count;
} __attribute__((packed));

#ifndef CGO
void govzf_event_Machine_deinit(uintptr_t vmHandle);
void govzf_event_Machine_onStateChange(uintptr_t vmHandle, int state);

void swext_proxy_cb_changed(void);
void swext_fsevents_cb_krpc_events(uint8_t* krpc_buf, size_t krpc_buf_len);
#endif