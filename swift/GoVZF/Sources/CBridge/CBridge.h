//
//  GoVZF.h
//  GoVZF
//
//  Created by Danny Lin on 3/3/23.
//

#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>

struct GResultCreate {
    void* ptr;
    char* err;
};

struct GResultErr {
    char* err;
};

struct GResultIntErr {
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

struct virtio_net_hdr_v1 {
    uint8_t flags;
    uint8_t gso_type;
    uint16_t hdr_len;
    uint16_t gso_size;
    uint16_t csum_start;
    uint16_t csum_offset;
    uint16_t num_buffers;
} __attribute__((packed));

#ifndef CGO
void govzf_event_Machine_deinit(uintptr_t vmHandle);
void govzf_event_Machine_onStateChange(uintptr_t vmHandle, int state);

void swext_proxy_cb_changed(void);
void swext_fsevents_cb_krpc_events(uint8_t* krpc_buf, size_t krpc_buf_len);

void swext_net_cb_path_changed(void);
#endif
