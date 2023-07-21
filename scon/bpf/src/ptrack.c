// Copyright 2023 Orbital Labs, LLC
// License: proprietary and confidential.

// Notify scon of bind() and released sockets.

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

#define IP4(a, b, c, d) ((a << 24) | (b << 16) | (c << 8) | d)
#define IP6(a,b,c,d,e,f,g,h) {bpf_htonl(a << 16 | b), bpf_htonl(c << 16 | d), bpf_htonl(e << 16 | f), bpf_htonl(g << 16 | h)}

#define LOCALHOST_IP4 IP4(127, 0, 0, 1)
static const __be32 LOCALHOST_IP6[4] = IP6(0, 0, 0, 0, 0, 0, 0, 1);

#define UNSPEC_IP4 0
static const __be32 UNSPEC_IP6[4] = IP6(0, 0, 0, 0, 0, 0, 0, 0);

const volatile __u64 config_netns_cookie = 0;

struct fwd_meta {
    // UDP notification is delayed until first recvmsg
    bool udp_notify_pending;
};

struct notify_event {
    __u8 unused;
};

// sk storage to indicate a tracked socket
struct {
	__uint(type, BPF_MAP_TYPE_SK_STORAGE);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, int);
	__type(value, struct fwd_meta);
} sk_meta_map SEC(".maps");

// ringbuf for notifications
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    // Go side debounces and reads /proc/net
    __uint(max_entries, 4096); // min - can't go lower
} notify_ring SEC(".maps");
// emit struct BTF
const struct notify_event *unused __attribute__((unused));

static bool check_netns(void *ctx) {
    __u64 cur_netns = bpf_get_netns_cookie(ctx);
    if (config_netns_cookie != cur_netns) {
        bpf_printk("not intended netns tgt=%llu cur=%llu", config_netns_cookie, cur_netns);
        return false;
    }

    return true;
}

static void send_notify() {
    struct notify_event event = {};
    int ret = bpf_ringbuf_output(&notify_ring, &event, sizeof(event), 0);
    if (ret != 0) {
        bpf_printk("failed to send notify");
    }
}

SEC("cgroup/sock_release")
int ptrack_sock_release(struct bpf_sock *sk) {
    // only intended netns
    if (!check_netns(sk)) {
        return VERDICT_PROCEED;
    }

    // only if tracked
    // no need to delete; it's done automatically by kernel on release
    if (bpf_sk_storage_get(&sk_meta_map, sk, NULL, 0) == NULL) {
        return VERDICT_PROCEED;
    }

    bpf_printk("sock_release");
    send_notify();

    return VERDICT_PROCEED;
}

static int recvmsg_common(struct bpf_sock_addr *ctx) {
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, NULL, 0);
    if (meta == NULL) {
        return VERDICT_PROCEED;
    }

    // if connect() called as client socket, sk storage will already be deleted
    if (meta->udp_notify_pending) {
        bpf_printk("recvmsg: first udp notify");
        send_notify();
        meta->udp_notify_pending = false;
    }

    return VERDICT_PROCEED;
}

/*
 * v4
 */
static bool check_ip4(struct bpf_sock_addr *ctx) {
    if (ctx->user_ip4 != bpf_htonl(LOCALHOST_IP4) && ctx->user_ip4 != bpf_htonl(UNSPEC_IP4)) {
        bpf_printk("not localhost or unspec %x", bpf_ntohl(ctx->user_ip4));
        return false;
    }

    return true;
}

SEC("cgroup/bind4")
int ptrack_bind4(struct bpf_sock_addr *ctx) {
    // only localhost
    if (!check_ip4(ctx)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(ctx)) {
        return VERDICT_PROCEED;
    }
    // only TCP or UDP
    if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM) {
        bpf_printk("not tcp or udp");
        return VERDICT_PROCEED;
    }

    // save to map
    struct fwd_meta init_meta = {};
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, &init_meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (meta == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_PROCEED;
    }

    // notify (TCP). UDP delayed until first recvmsg
    if (ctx->type == SOCK_STREAM) {
        send_notify();
    } else {
        meta->udp_notify_pending = true;
    }

    bpf_printk("bind4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return VERDICT_PROCEED;
}

SEC("cgroup/connect4")
int ptrack_connect4(struct bpf_sock_addr *ctx) {
    if (bpf_sk_storage_delete(&sk_meta_map, ctx->sk) == 0) {
        bpf_printk("connect4: deleted sk %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    }

    return VERDICT_PROCEED;
}

SEC("cgroup/recvmsg4")
int ptrack_recvmsg4(struct bpf_sock_addr *ctx) {
    return recvmsg_common(ctx);
}

/*
 * v6
 */
static bool check_ip6(struct bpf_sock_addr *ctx) {
    if (memcmp(ctx->user_ip6, LOCALHOST_IP6, 16) != 0 && memcmp(ctx->user_ip6, UNSPEC_IP6, 16) != 0) {
        bpf_printk("not localhost or unspec %08x%08x%08x%08x", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]));
        return false;
    }

    return true;
}

SEC("cgroup/bind6")
int ptrack_bind6(struct bpf_sock_addr *ctx) {
    // only localhost
    if (!check_ip6(ctx)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(ctx)) {
        return VERDICT_PROCEED;
    }
    // only TCP or UDP
    if (ctx->type != SOCK_STREAM && ctx->type != SOCK_DGRAM) {
        bpf_printk("not tcp or udp");
        return VERDICT_PROCEED;
    }

    // save to map
    struct fwd_meta meta = {};
    struct fwd_meta *ret = bpf_sk_storage_get(&sk_meta_map, ctx->sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (ret == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_REJECT;
    }

    // notify (TCP). UDP delayed until first recvmsg
    if (ctx->type == SOCK_STREAM) {
        send_notify();
    } else {
        ret->udp_notify_pending = true;
    }

    bpf_printk("bind6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return VERDICT_PROCEED;
}

SEC("cgroup/connect6")
int ptrack_connect6(struct bpf_sock_addr *ctx) {
    if (bpf_sk_storage_delete(&sk_meta_map, ctx->sk) == 0) {
        bpf_printk("connect6: deleted sk %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    }

    return VERDICT_PROCEED;
}

SEC("cgroup/recvmsg6")
int ptrack_recvmsg6(struct bpf_sock_addr *ctx) {
    return recvmsg_common(ctx);
}

#ifdef DEBUG
char _license[] SEC("license") = "GPL";
#else
char _license[] SEC("license") = "Proprietary";
#endif
