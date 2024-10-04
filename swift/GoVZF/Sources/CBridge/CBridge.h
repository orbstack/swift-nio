//
//  GoVZF.h
//  GoVZF
//
//  Created by Danny Lin on 3/3/23.
//

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/uio.h>

struct GResultCreate {
    void *ptr;
    char *err;
};

struct GResultErr {
    char *err;
};

struct GResultIntErr {
    int64_t value;
    char *err;
};

struct krpc_header {
    uint32_t len;
    uint32_t typ;
} __attribute__((packed));

struct krpc_notifyproxy_inject {
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

// to avoid allocation in Swift receive path
struct two_iovecs {
    struct iovec iovs[2];
};

#ifndef CGO
void govzf_event_Machine_deinit(uintptr_t vmHandle);
void govzf_event_Machine_onStateChange(uintptr_t vmHandle, int state);

int rsvm_network_write_packet(uintptr_t handle, const struct iovec *iovs, size_t num_iovs,
                              size_t total_len);

void swext_proxy_cb_changed(void);
void swext_fsevents_cb_krpc_events(uint8_t *krpc_buf, size_t krpc_buf_len);

void swext_net_cb_path_changed(void);
#endif
