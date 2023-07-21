// Copyright 2023 Orbital Labs, LLC
// License: proprietary and confidential.

// Notify scon of bind() and released sockets.
// UDP cases:
//   - bind -> sendmsg: consider client, don't notify
//   - bind -> recvmsg: consider server, notify
//   - bind -> (nothing): debounce 20 ms, then notify and assume server

#include <string.h>
#include <stdbool.h>

#include <linux/stddef.h>
#include <linux/bpf.h>
#include <linux/in.h>
#include <linux/in6.h>
#include <linux/if.h>
#include <errno.h>
#include <time.h>

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

#define UDP_BIND_DEBOUNCE 20 // ms

const volatile __u64 config_netns_cookie = 0;

struct fwd_meta {
    // UDP notification is delayed until first recvmsg
    bool has_udp_meta;
    bool udp_notify_pending;
};

struct udp_meta {
    struct bpf_timer notify_timer;
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

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u64);
    __type(value, struct udp_meta);
} udp_meta_map SEC(".maps");

// ringbuf for notifications
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    // Go side debounces and reads /proc/net
    __uint(max_entries, 4096); // min - can't go lower
} notify_ring SEC(".maps");

static bool check_netns(void *ctx) {
    __u64 cur_netns = bpf_get_netns_cookie(ctx);
    if (config_netns_cookie != cur_netns) {
        bpf_printk("not intended netns tgt=%llu cur=%llu", config_netns_cookie, cur_netns);
        return false;
    }

    return true;
}

static void send_notify() {
    bpf_printk("*** notify");
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
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, sk, NULL, 0);
    if (meta == NULL) {
        return VERDICT_PROCEED;
    }

    bpf_printk("sock_release");
    if (meta->udp_notify_pending) {
        __u64 cookie = bpf_get_socket_cookie(sk);
        bpf_map_delete_elem(&udp_meta_map, &cookie);
    }
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
        __u64 cookie = bpf_get_socket_cookie(ctx);
        int ret = bpf_map_delete_elem(&udp_meta_map, &cookie);
        if (ret == 0) {
            // if delete failed, timer already fired, so no need to notify again
            send_notify();
        }
        meta->udp_notify_pending = false;
    }

    return VERDICT_PROCEED;
}

static int sendmsg_common(struct bpf_sock_addr *ctx) {
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, NULL, 0);
    if (meta == NULL) {
        return VERDICT_PROCEED;
    }

    // if sendmsg() called before first recvmsg(), then this is probably a client socket, not server
    // leave the sk storage. this could still be a server socket if this isn't the first sendmsg
    if (meta->udp_notify_pending) {
        bpf_printk("sendmsg: clear pending");
        __u64 cookie = bpf_get_socket_cookie(ctx);
        bpf_map_delete_elem(&udp_meta_map, &cookie);
        meta->udp_notify_pending = false;
    }

    return VERDICT_PROCEED;
}

static int udp_timer_cb(void *map, int *key, struct udp_meta *val) {
    bpf_printk("udp debounce fired");
    send_notify();

    // delete self to clear timer
    int ret = bpf_map_delete_elem(map, key);
    if (ret != 0) {
        bpf_printk("failed to delete udp meta");
    }

    return 0;
}

/*
 * v4
 */
static bool check_ip4(struct bpf_sock *sk) {
    if (sk->src_ip4 != bpf_htonl(LOCALHOST_IP4) && sk->src_ip4 != bpf_htonl(UNSPEC_IP4)) {
        bpf_printk("not localhost or unspec %x", bpf_ntohl(sk->src_ip4));
        return false;
    }

    return true;
}

SEC("cgroup/post_bind4")
int ptrack_post_bind4(struct bpf_sock *sk) {
    // only localhost
    if (!check_ip4(sk)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(sk)) {
        return VERDICT_PROCEED;
    }
    // only TCP or UDP
    if (sk->type != SOCK_STREAM && sk->type != SOCK_DGRAM) {
        bpf_printk("not tcp or udp");
        return VERDICT_PROCEED;
    }

    // save to map
    struct fwd_meta init_meta = {};
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, sk, &init_meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (meta == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_PROCEED;
    }

    // notify (TCP). UDP delayed until first recvmsg
    if (sk->type == SOCK_STREAM) {
        send_notify();
    } else {
        meta->udp_notify_pending = true;

        // start timer
        struct udp_meta init_udp = {};
        __u64 cookie = bpf_get_socket_cookie(sk);
        bpf_map_update_elem(&udp_meta_map, &cookie, &init_udp, BPF_ANY);
        struct udp_meta *udp = bpf_map_lookup_elem(&udp_meta_map, &cookie);
        if (udp == NULL) {
            bpf_printk("failed to lookup udp meta");
            return VERDICT_PROCEED;
        }

        bpf_timer_init(&udp->notify_timer, &udp_meta_map, CLOCK_MONOTONIC);
        bpf_timer_set_callback(&udp->notify_timer, udp_timer_cb);
        bpf_timer_start(&udp->notify_timer, UDP_BIND_DEBOUNCE * 1000 * 1000, 0);
    }

    bpf_printk("post_bind4: %x:%d", bpf_ntohl(sk->src_ip4), sk->src_port);
    return VERDICT_PROCEED;
}

SEC("cgroup/connect4")
int ptrack_connect4(struct bpf_sock_addr *ctx) {
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, NULL, 0);
    if (meta == NULL) {
        return VERDICT_PROCEED;
    }

    // delete timer
    if (meta->udp_notify_pending) {
        __u64 cookie = bpf_get_socket_cookie(ctx);
        bpf_map_delete_elem(&udp_meta_map, &cookie);
    }

    bpf_printk("connect4: delete sk %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    bpf_sk_storage_delete(&sk_meta_map, ctx->sk);

    return VERDICT_PROCEED;
}

SEC("cgroup/recvmsg4")
int ptrack_recvmsg4(struct bpf_sock_addr *ctx) {
    bpf_printk("recvmsg4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return recvmsg_common(ctx);
}

SEC("cgroup/sendmsg4")
int ptrack_sendmsg4(struct bpf_sock_addr *ctx) {
    bpf_printk("sendmsg4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return sendmsg_common(ctx);
}

/*
 * v6
 */
static bool check_ip6(struct bpf_sock *sk) {
    if (memcmp(sk->src_ip6, LOCALHOST_IP6, 16) != 0 && memcmp(sk->src_ip6, UNSPEC_IP6, 16) != 0) {
        bpf_printk("not localhost or unspec %08x%08x%08x%08x", bpf_ntohl(sk->src_ip6[0]), bpf_ntohl(sk->src_ip6[1]), bpf_ntohl(sk->src_ip6[2]), bpf_ntohl(sk->src_ip6[3]));
        return false;
    }

    return true;
}

SEC("cgroup/post_bind6")
int ptrack_post_bind6(struct bpf_sock *sk) {
    // only localhost
    if (!check_ip6(sk)) {
        return VERDICT_PROCEED;
    }
    // only intended netns
    if (!check_netns(sk)) {
        return VERDICT_PROCEED;
    }
    // only TCP or UDP
    if (sk->type != SOCK_STREAM && sk->type != SOCK_DGRAM) {
        bpf_printk("not tcp or udp");
        return VERDICT_PROCEED;
    }

    // save to map
    struct fwd_meta meta = {};
    struct fwd_meta *ret = bpf_sk_storage_get(&sk_meta_map, sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (ret == NULL) {
        bpf_printk("failed to save meta");
        return VERDICT_REJECT;
    }

    // notify (TCP). UDP delayed until first recvmsg
    if (sk->type == SOCK_STREAM) {
        send_notify();
    } else {
        ret->udp_notify_pending = true;
    }

    bpf_printk("post_bind6: %08x%08x%08x%08x:%d", bpf_ntohl(sk->src_ip6[0]), bpf_ntohl(sk->src_ip6[1]), bpf_ntohl(sk->src_ip6[2]), bpf_ntohl(sk->src_ip6[3]), sk->src_port);
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
    bpf_printk("recvmsg6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return recvmsg_common(ctx);
}

SEC("cgroup/sendmsg6")
int ptrack_sendmsg6(struct bpf_sock_addr *ctx) {
    bpf_printk("sendmsg6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return sendmsg_common(ctx);
}

#ifdef DEBUG
char _license[] SEC("license") = "GPL";
#else
char _license[] SEC("license") = "Proprietary";
#endif
