// Copyright 2024 Orbital Labs, LLC
// License: proprietary and confidential.

#include <linux/bpf.h>
#include <linux/stddef.h>
#include <linux/if.h>

#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// warning: this makes it GPL
// #define DEBUG

#ifndef DEBUG
#ifdef bpf_printk
#undef bpf_printk
#endif
#define bpf_printk(fmt, ...) do {} while (0)
#endif

#define IP4(a, b, c, d) (bpf_htonl((a << 24) | (b << 16) | (c << 8) | d))

const volatile __u8  config_tproxy_subnet4_enabled = 0; // bool
const volatile __u32 config_tproxy_subnet4_ip = 0; // network order
const volatile __u32 config_tproxy_subnet4_mask = 0xffffffff; // network order
const volatile __u8  config_tproxy_subnet6_enabled = 0; // bool
const volatile __u32 config_tproxy_subnet6_ip[4] = {0, 0, 0, 0}; // network order
const volatile __u32 config_tproxy_subnet6_mask[4] = {0xffffffff, 0xffffffff, 0xffffffff, 0xffffffff}; // network order

// 0 port means any port
const volatile __u16 config_tproxy_port = 0; // host order

const volatile __u32 config_tproxy_socket_key4 = 0;
const volatile __u32 config_tproxy_socket_key6 = 1;
struct {
    __uint(type, BPF_MAP_TYPE_SOCKMAP);
    __uint(max_entries, 2);
    __type(key, __u32);
    __type(value, __u64);
} tproxy_socket SEC(".maps");

static __always_inline __u8 matches_subnet4(volatile __u32 ip, volatile __u32 subnet, volatile __u32 mask) {
    return (ip & mask) == subnet;
}

static __always_inline __u8 matches_subnet6(volatile __u32 *ip, volatile __u32 *subnet, volatile __u32 *mask) {
    #pragma clang loop unroll(enable)
    for (int i = 0; i < 4; i++) {
        if ((ip[i] & mask[i]) != subnet[i]) {
            return 0;
        }
    }
    return 1;
}

SEC("sk_lookup/")
int tproxy_sk_lookup(struct bpf_sk_lookup *ctx) {
    struct bpf_sock *sk = NULL;

    if (ctx->family == AF_INET
            && config_tproxy_subnet4_enabled
            && (!config_tproxy_port || ctx->local_port == config_tproxy_port)
            && matches_subnet4(ctx->local_ip4, config_tproxy_subnet4_ip, config_tproxy_subnet4_mask)) {
        bpf_printk("tproxy | ipv4 match");

        sk = bpf_map_lookup_elem(&tproxy_socket, (const void*) &config_tproxy_socket_key4);
    } else if (ctx->family == AF_INET6
            && config_tproxy_subnet6_enabled
            && (!config_tproxy_port || ctx->local_port == config_tproxy_port)
            && matches_subnet6(ctx->local_ip6, (volatile __u32*) config_tproxy_subnet6_ip, (volatile __u32*) config_tproxy_subnet6_mask)) {
        bpf_printk("tproxy | ipv6 match");

        sk = bpf_map_lookup_elem(&tproxy_socket, (const void*) &config_tproxy_socket_key6);
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