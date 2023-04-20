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

enum {
    VERDICT_REJECT = 0,
    VERDICT_PROCEED = 1,
};

#define LOCALHOST_IP4 0x7f000001
static const __be32 LOCALHOST_IP6[4] = {bpf_htonl(0x00000000), bpf_htonl(0x00000000), bpf_htonl(0x00000000), bpf_htonl(0x00000001)};

// hostnat ip = 100.115.92.254
#define HOSTNAT_IP4 0x64735cfe
// ipv6 = fd00:96dc:7096:1d22::254
static const __be32 HOSTNAT_IP6[4] = {bpf_htonl(0xfd0096dc), bpf_htonl(0x70961d22), bpf_htonl(0x00000000), bpf_htonl(0x00000254)};

const volatile __u64 config_netns_cookie = 0;

struct fwd_meta {
    // value size can't be 0
    __u8 unused;
};

// sk storage to translate addr for getpeername
struct {
	__uint(type, BPF_MAP_TYPE_SK_STORAGE);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, int);
	__type(value, struct fwd_meta);
} sk_meta_map SEC(".maps");

static bool check_netns(struct bpf_sock_addr *ctx) {
    __u64 cur_netns = bpf_get_netns_cookie(ctx);
    if (config_netns_cookie != cur_netns) {
        bpf_printk("not intended netns tgt=%llu cur=%llu", config_netns_cookie, cur_netns);
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

static bool check_listener4(struct bpf_sock_addr *ctx, __be32 udp_src_ip4) {
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
        bpf_printk("lookup tcp: %x:%d", bpf_ntohl(tuple.ipv4.daddr), bpf_ntohs(tuple.ipv4.dport));
        // skc lookup includes timewait and request
        sk = bpf_skc_lookup_tcp(ctx, &tuple, sizeof(tuple.ipv4), BPF_F_CURRENT_NETNS, 0);
    } else if (ctx->type == SOCK_DGRAM) {
        bpf_printk("lookup udp: %x:%d -> %x:%d", bpf_ntohl(udp_src_ip4), ctx->sk->src_port, bpf_ntohl(tuple.ipv4.daddr), bpf_ntohs(tuple.ipv4.dport));
        if (udp_src_ip4 != 0) {
            tuple.ipv4.saddr = udp_src_ip4;
            tuple.ipv4.sport = bpf_htons(ctx->sk->src_port);
        }
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
    // strange things will happen with port 0
    if (ctx->user_port == 0) {
        bpf_printk("port 0");
        return VERDICT_PROCEED;
    }

    // check for existing socket
    if (!check_listener4(ctx, 0)) {
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

    bpf_printk("redirecting tcp4/udp4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
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
    // strange things will happen with port 0
    if (ctx->user_port == 0) {
        bpf_printk("port 0");
        return VERDICT_PROCEED;
    }

    bpf_printk("sendmsg4: ip=%x srcip=%x port=%d state=%d", bpf_ntohl(ctx->user_ip4), bpf_ntohl(ctx->msg_src_ip4), bpf_ntohs(ctx->user_port), ctx->sk->state);

    // check for existing socket
    // combinations: src 0.0.0.0 (unlikely for client), src 127.0.0.1 (likely since dest is localhost), and explicit src
    if (!check_listener4(ctx, 0) || !check_listener4(ctx, bpf_htonl(LOCALHOST_IP4))) {
        return VERDICT_PROCEED;
    }
    // also check for explicit sendmsg src ip4 if needed
    if (ctx->msg_src_ip4 != 0 && !check_listener4(ctx, ctx->msg_src_ip4)) {
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
static bool check_ip6(struct bpf_sock_addr *ctx) {
    if (memcmp(ctx->user_ip6, LOCALHOST_IP6, 16)) {
        bpf_printk("not localhost %08x%08x%08x%08x", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]));

        // udp sockets can be reconnected, so delete meta just in case
        if (ctx->type == SOCK_DGRAM) {
            bpf_sk_storage_delete(&sk_meta_map, ctx->sk);
        }

        return false;
    }

    return true;
}

#define copy4(dst, src) \
    dst[0] = src[0]; \
    dst[1] = src[1]; \
    dst[2] = src[2]; \
    dst[3] = src[3];

// inline needed for copy from *udp_src_ip6
static __always_inline bool check_listener6(struct bpf_sock_addr *ctx, const __be32 *udp_src_ip6) {
    struct bpf_sock_tuple tuple = {
        .ipv6 = {
            .saddr = {0},
            .sport = 0,
            .dport = ctx->user_port,
        },
    };
    // copy daddr (verifier doesn't like memcpy)
    copy4(tuple.ipv6.daddr, ctx->user_ip6);

    struct bpf_sock *sk;
    if (ctx->type == SOCK_STREAM) {
        bpf_printk("lookup tcp: %08x%08x%08x%08x:%d", bpf_ntohl(tuple.ipv6.daddr[0]), bpf_ntohl(tuple.ipv6.daddr[1]), bpf_ntohl(tuple.ipv6.daddr[2]), bpf_ntohl(tuple.ipv6.daddr[3]), bpf_ntohs(tuple.ipv6.dport));
        // skc lookup includes timewait and request
        sk = bpf_skc_lookup_tcp(ctx, &tuple, sizeof(tuple.ipv6), BPF_F_CURRENT_NETNS, 0);
    } else if (ctx->type == SOCK_DGRAM) {
        if (udp_src_ip6 != NULL) {
            copy4(tuple.ipv6.saddr, udp_src_ip6);
            tuple.ipv6.sport = bpf_htons(ctx->sk->src_port);
        }
        bpf_printk("lookup udp: %08x%08x%08x%08x:%d -> %08x%08x%08x%08x:%d", bpf_ntohl(tuple.ipv6.saddr[0]), bpf_ntohl(tuple.ipv6.saddr[1]), bpf_ntohl(tuple.ipv6.saddr[2]), bpf_ntohl(tuple.ipv6.saddr[3]), ctx->sk->src_port, bpf_ntohl(tuple.ipv6.daddr[0]), bpf_ntohl(tuple.ipv6.daddr[1]), bpf_ntohl(tuple.ipv6.daddr[2]), bpf_ntohl(tuple.ipv6.daddr[3]), bpf_ntohs(tuple.ipv6.dport));
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
    // strange things will happen with port 0
    if (ctx->user_port == 0) {
        bpf_printk("port 0");
        return VERDICT_PROCEED;
    }

    // check for existing socket
    if (!check_listener6(ctx, NULL)) {
        return VERDICT_PROCEED;
    }

    // rewrite address
    copy4(ctx->user_ip6, HOSTNAT_IP6);

    // save to map
    struct fwd_meta meta = {};
    struct fwd_meta *ret = bpf_sk_storage_get(&sk_meta_map, ctx->sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (ret == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_REJECT;
    }

    bpf_printk("redirecting tcp6/udp6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
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
    // strange things will happen with port 0
    if (ctx->user_port == 0) {
        bpf_printk("port 0");
        return VERDICT_PROCEED;
    }

    bpf_printk("sendmsg6: ip=%08x%08x%08x%08x port=%d state=%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port), ctx->sk->state);

    // check for existing socket
    // combinations: src :: (unlikely for client), src ::1 (likely since dest is localhost), and explicit src
    if (!check_listener6(ctx, NULL) || !check_listener6(ctx, LOCALHOST_IP6)) {
        return VERDICT_PROCEED;
    }
    // also check for explicit sendmsg src ip6 if needed
    bool has_explicit_src =
        ctx->msg_src_ip6[0] != 0 ||
        ctx->msg_src_ip6[1] != 0 ||
        ctx->msg_src_ip6[2] != 0 ||
        ctx->msg_src_ip6[3] != 0;
    if (has_explicit_src && !check_listener6(ctx, (__be32*) &ctx->msg_src_ip6)) {
        return VERDICT_PROCEED;
    }

    // rewrite address
    copy4(ctx->user_ip6, HOSTNAT_IP6);

    // don't save to map. this is an unconnected udp socket, so no getpeername

    bpf_printk("redirecting udp6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
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
    copy4(ctx->user_ip6, LOCALHOST_IP6);
    return VERDICT_PROCEED;
}

#ifdef DEBUG
char _license[] SEC("license") = "GPL";
#else
char _license[] SEC("license") = "Proprietary";
#endif
