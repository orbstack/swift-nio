// Copyright 2024 Orbital Labs, LLC
// License: proprietary and confidential.

#include <linux/bpf.h>
#include <linux/if.h>
#include <linux/stddef.h>
#include <stdbool.h>

#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

// warning: this makes it GPL
// #define DEBUG

#ifndef DEBUG
#ifdef bpf_printk
#undef bpf_printk
#endif
#define bpf_printk(fmt, ...) \
    do { \
    } while (0)
#endif

#define IP4(a, b, c, d) (bpf_htonl((a << 24) | (b << 16) | (c << 8) | d))

const volatile __u8 config_tproxy_subnet4_enabled = 0;           // bool
const volatile __u32 config_tproxy_subnet4_ip = 0;               // network order
const volatile __u32 config_tproxy_subnet4_mask = 0xffffffff;    // network order

const volatile __u8 config_tproxy_subnet6_enabled = 0;           // bool
const volatile __u32 config_tproxy_subnet6_ip[4] = {0, 0, 0, 0}; // network order
const volatile __u32 config_tproxy_subnet6_mask[4] = {0xffffffff, 0xffffffff, 0xffffffff,
                                                      0xffffffff}; // network order

// 0 = disabled
#define MAX_PORTS 2
const volatile __u16 config_tproxy_ports[MAX_PORTS] = {0, 0};

#define SOCKET_KEY4 0
#define SOCKET_KEY6 1
#define SOCKET_KEY_MAX 2

struct {
    __uint(type, BPF_MAP_TYPE_SOCKMAP);
    __uint(max_entries, MAX_PORTS * SOCKET_KEY_MAX);
    __type(key, __u32);
    __type(value, __u64);
} tproxy_socket SEC(".maps");

static __always_inline bool matches_subnet4(__u32 ip, __u32 subnet, __u32 mask) {
    return (ip & mask) == subnet;
}

static __always_inline bool matches_subnet6(const __u32 *ip, const volatile __u32 *subnet,
                                            const volatile __u32 *mask) {
#pragma clang loop unroll(enable)
    for (int i = 0; i < 4; i++) {
        if ((ip[i] & mask[i]) != subnet[i]) {
            return 0;
        }
    }
    return 1;
}

static __always_inline bool matches_port(__u16 port, int *socket_index) {
#pragma clang loop unroll(enable)
    for (int i = 0; i < MAX_PORTS; i++) {
        if (config_tproxy_ports[i] != 0 && config_tproxy_ports[i] == port) {
            *socket_index = i * SOCKET_KEY_MAX;
            return true;
        }
    }
    return false;
}

SEC("sk_lookup/")
int tproxy_sk_lookup(struct bpf_sk_lookup *ctx) {
    struct bpf_sock *sk = NULL;
    __u32 socket_index = 0;

    if (ctx->family == AF_INET && config_tproxy_subnet4_enabled &&
        matches_port(ctx->local_port, &socket_index) &&
        matches_subnet4(ctx->local_ip4, config_tproxy_subnet4_ip, config_tproxy_subnet4_mask)) {
        bpf_printk("tproxy | ipv4 match");

        socket_index += SOCKET_KEY4;
        sk = bpf_map_lookup_elem(&tproxy_socket, (const void *)&socket_index);
    } else if (ctx->family == AF_INET6 && config_tproxy_subnet6_enabled &&
               matches_port(ctx->local_port, &socket_index) &&
               matches_subnet6(ctx->local_ip6, config_tproxy_subnet6_ip,
                               config_tproxy_subnet6_mask)) {
        bpf_printk("tproxy | ipv6 match");

        socket_index += SOCKET_KEY6;
        sk = bpf_map_lookup_elem(&tproxy_socket, (const void *)&socket_index);
    }

    if (!sk) {
        return SK_PASS;
    }

    bpf_printk("tproxy | assigning sk");
    int ret = bpf_sk_assign(ctx, sk, 0);
    if (ret) {
        bpf_printk("tproxy | failed to assign sk: %d", ret);
    }

    bpf_sk_release(sk);

    return SK_PASS;
}

#ifdef DEBUG
char _license[] SEC("license") = "GPL";
#else
char _license[] SEC("license") = "Proprietary";
#endif
