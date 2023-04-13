// Redirects connect() calls to localhost (127.0.0.1 or ::1) to host NAT
// (100.115.92.254) if there's no listener on localhost.
// Also translates getpeername() to return localhost so programs don't get confused,
// and sendmsg() for UDP.

#include <string.h>
#include <stdbool.h>

#include <linux/stddef.h>
#include <linux/bpf.h>
#include <linux/in.h>
#include <linux/in6.h>
#include <linux/if.h>
#include <errno.h>

#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// warning: this makes it GPL
//#define DEBUG

#ifndef DEBUG
#ifdef bpf_printk
#undef bpf_printk
#endif
#define bpf_printk(fmt, ...) do { } while (0)
#endif

#define VERDICT_REJECT	0
#define VERDICT_PROCEED	1

#define LOCALHOST_IP4 0x7f000001
#define LOCALHOST_IP6_0 0x00000000
#define LOCALHOST_IP6_1 0x00000000
#define LOCALHOST_IP6_2 0x00000000
#define LOCALHOST_IP6_3 0x00000001

// hostnat ip = 100.115.92.254
#define HOSTNAT_IP4 0x64735cfe
// ipv6 = fd00:96dc:7096:1d22::254
#define HOSTNAT_IP6_0 0xfd0096dc
#define HOSTNAT_IP6_1 0x70961d22
#define HOSTNAT_IP6_2 0x00000000
#define HOSTNAT_IP6_3 0x00000254

struct fwd_meta {
    // value size can't be 0
    int unused;
};

// config map, for netns cookie
struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, __u32);
    __type(value, __u64);
    __uint(max_entries, 1);
} config_map SEC(".maps");

// sk storage to translate addr for getpeername
struct {
	__uint(type, BPF_MAP_TYPE_SK_STORAGE);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, int);
	__type(value, struct fwd_meta);
} sk_meta_map SEC(".maps");

static bool check_netns(struct bpf_sock_addr *ctx) {
    __u64 cur_netns = bpf_get_netns_cookie(ctx);
    __u32 zero_key = 0;
    __u64 *netns = bpf_map_lookup_elem(&config_map, &zero_key);
    if (netns == NULL || *netns != cur_netns) {
        if (netns == NULL) {
            bpf_printk("config netns not set");
        } else {
            bpf_printk("not intended netns tgt=%llu cur=%llu", *netns, cur_netns);
        }
        return false;
    }

    return true;
}

/*
 * v4
 */
static bool check_ip4(struct bpf_sock_addr *ctx) {
    if (ctx->user_ip4 != bpf_htonl(LOCALHOST_IP4)) {
        bpf_printk("not localhost %x", bpf_ntohl(ctx->user_ip4));
        
        // udp sockets can be reconnected, so delete meta just in case
        if (ctx->type == SOCK_DGRAM) {
            bpf_sk_storage_delete(&sk_meta_map, ctx->sk);
        }

        return false;
    }

    return true;
}

static bool check_listener4(struct bpf_sock_addr *ctx) {
    struct bpf_sock_tuple tuple = {
        .ipv4 = {
            .saddr = 0,
            .sport = 0,
            .daddr = ctx->user_ip4,
            .dport = ctx->user_port,
        },
    };

    struct bpf_sock *sk;
    if (ctx->type == SOCK_STREAM) {
        // skc lookup includes timewait and request
        sk = bpf_skc_lookup_tcp(ctx, &tuple, sizeof(tuple.ipv4), BPF_F_CURRENT_NETNS, 0);
    } else if (ctx->type == SOCK_DGRAM) {
        sk = bpf_sk_lookup_udp(ctx, &tuple, sizeof(tuple.ipv4), BPF_F_CURRENT_NETNS, 0);
    } else {
        bpf_printk("unknown socket type %d", ctx->type);
        return false;
    }
    if (sk != NULL) {
        bpf_printk("found existing socket");
        bpf_sk_release(sk);
        return false;
    }

    return true;
}

SEC("cgroup/connect4")
int lfwd_connect4(struct bpf_sock_addr *ctx) {
    // only localhost
    if (!check_ip4(ctx)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(ctx)) {
        return VERDICT_PROCEED;
    }

    // check for existing socket
    if (!check_listener4(ctx)) {
        return VERDICT_PROCEED;
    }

    // rewrite address
    ctx->user_ip4 = bpf_htonl(HOSTNAT_IP4);

    // save to map
    struct fwd_meta meta = {};
    struct fwd_meta *ret = bpf_sk_storage_get(&sk_meta_map, ctx->sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (ret == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_REJECT;
    }

    bpf_printk("redirecting tcp4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return VERDICT_PROCEED;
}

SEC("cgroup/sendmsg4")
int lfwd_sendmsg4(struct bpf_sock_addr *ctx) {
    // only localhost
    if (!check_ip4(ctx)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(ctx)) {
        return VERDICT_PROCEED;
    }

    bpf_printk("sendmsg4: ip=%x port=%d state=%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port), ctx->sk->state);

    // check for existing socket
    if (!check_listener4(ctx)) {
        return VERDICT_PROCEED;
    }

    // rewrite address
    ctx->user_ip4 = bpf_htonl(HOSTNAT_IP4);

    // don't save to map. this is an unconnected udp socket, so no getpeername

    bpf_printk("redirecting udp4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return VERDICT_PROCEED;
}

SEC("cgroup/getpeername4")
int lfwd_getpeername4(struct bpf_sock_addr *ctx) {
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, 0, 0);
    if (meta == NULL) {
        bpf_printk("no meta");
        return VERDICT_PROCEED;
    }

    // rewrite address
    ctx->user_ip4 = bpf_htonl(LOCALHOST_IP4);

    return VERDICT_PROCEED;
}

/*
 * v6
 */
static bool ip6_eq4(__u32 *a, __u32 b0, __u32 b1, __u32 b2, __u32 b3) {
    return a[0] == bpf_htonl(b0) && a[1] == bpf_htonl(b1) && a[2] == bpf_htonl(b2) && a[3] == bpf_htonl(b3);
}

static bool check_ip6(struct bpf_sock_addr *ctx) {
    if (!ip6_eq4(ctx->user_ip6, LOCALHOST_IP6_0, LOCALHOST_IP6_1, LOCALHOST_IP6_2, LOCALHOST_IP6_3)) {
        bpf_printk("not localhost %x%x%x%x", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]));

        // udp sockets can be reconnected, so delete meta just in case
        if (ctx->type == SOCK_DGRAM) {
            bpf_sk_storage_delete(&sk_meta_map, ctx->sk);
        }

        return false;
    }

    return true;
}

static bool check_listener6(struct bpf_sock_addr *ctx) {
    struct bpf_sock_tuple tuple = {
        .ipv6 = {
            .saddr = {0},
            .sport = 0,
            .dport = ctx->user_port,
        },
    };
    // copy daddr (verifier doesn't like memcpy)
    tuple.ipv6.daddr[0] = ctx->user_ip6[0];
    tuple.ipv6.daddr[1] = ctx->user_ip6[1];
    tuple.ipv6.daddr[2] = ctx->user_ip6[2];
    tuple.ipv6.daddr[3] = ctx->user_ip6[3];

    struct bpf_sock *sk;
    if (ctx->type == SOCK_STREAM) {
        // skc lookup includes timewait and request
        sk = bpf_skc_lookup_tcp(ctx, &tuple, sizeof(tuple.ipv6), BPF_F_CURRENT_NETNS, 0);
    } else if (ctx->type == SOCK_DGRAM) {
        sk = bpf_sk_lookup_udp(ctx, &tuple, sizeof(tuple.ipv6), BPF_F_CURRENT_NETNS, 0);
    } else {
        bpf_printk("unknown socket type %d", ctx->type);
        return false;
    }
    if (sk != NULL) {
        bpf_printk("found existing socket");
        bpf_sk_release(sk);
        return false;
    }

    return true;
}

SEC("cgroup/connect6")
int lfwd_connect6(struct bpf_sock_addr *ctx) {
    // only localhost
    if (!check_ip6(ctx)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(ctx)) {
        return VERDICT_PROCEED;
    }

    // check for existing socket
    if (!check_listener6(ctx)) {
        return VERDICT_PROCEED;
    }

    // rewrite address
    ctx->user_ip6[0] = bpf_htonl(HOSTNAT_IP6_0);
    ctx->user_ip6[1] = bpf_htonl(HOSTNAT_IP6_1);
    ctx->user_ip6[2] = bpf_htonl(HOSTNAT_IP6_2);
    ctx->user_ip6[3] = bpf_htonl(HOSTNAT_IP6_3);

    // save to map
    struct fwd_meta meta = {};
    struct fwd_meta *ret = bpf_sk_storage_get(&sk_meta_map, ctx->sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (ret == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_REJECT;
    }

    bpf_printk("redirecting tcp6: %x%x%x%x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return VERDICT_PROCEED;
}

SEC("cgroup/sendmsg6")
int lfwd_sendmsg6(struct bpf_sock_addr *ctx) {
    // only localhost
    if (!check_ip6(ctx)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(ctx)) {
        return VERDICT_PROCEED;
    }

    bpf_printk("sendmsg6: ip=%x%x%x%x port=%d state=%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port), ctx->sk->state);

    // check for existing socket
    if (!check_listener6(ctx)) {
        return VERDICT_PROCEED;
    }

    // rewrite address
    ctx->user_ip6[0] = bpf_htonl(HOSTNAT_IP6_0);
    ctx->user_ip6[1] = bpf_htonl(HOSTNAT_IP6_1);
    ctx->user_ip6[2] = bpf_htonl(HOSTNAT_IP6_2);
    ctx->user_ip6[3] = bpf_htonl(HOSTNAT_IP6_3);

    // don't save to map. this is an unconnected udp socket, so no getpeername

    bpf_printk("redirecting udp6: %x%x%x%x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return VERDICT_PROCEED;
}

SEC("cgroup/getpeername6")
int lfwd_getpeername6(struct bpf_sock_addr *ctx) {
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, 0, 0);
    if (meta == NULL) {
        bpf_printk("no meta");
        return VERDICT_PROCEED;
    }

    // rewrite address
    ctx->user_ip6[0] = bpf_htonl(LOCALHOST_IP6_0);
    ctx->user_ip6[1] = bpf_htonl(LOCALHOST_IP6_1);
    ctx->user_ip6[2] = bpf_htonl(LOCALHOST_IP6_2);
    ctx->user_ip6[3] = bpf_htonl(LOCALHOST_IP6_3);

    return VERDICT_PROCEED;
}

#ifdef DEBUG
char _license[] SEC("license") = "GPL";
#else
char _license[] SEC("license") = "Proprietary";
#endif
