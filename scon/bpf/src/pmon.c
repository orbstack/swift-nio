// Copyright 2023 Orbital Labs, LLC
// License: proprietary and confidential.

// Notify scon of bind() and released sockets.
// UDP cases:
//   - bind -> sendmsg: consider client, don't notify
//   - bind -> recvmsg: consider server, notify
//   - bind -> (nothing): debounce 20 ms, then notify and assume server
// Test cases:
//   - socat (uses select, no recv): socat STDIO UDP-LISTEN:11112
//   - Traefik + CoreDNS in Docker Compose net=host
//   - dig and curl DNS clients

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
#include <bpf/bpf_tracing.h>

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

enum {
    LTYPE_TCP = 1 << 0,
    LTYPE_UDP = 1 << 1,
    LTYPE_IPTABLES = 1 << 2,
};

#define IP4(a, b, c, d) (bpf_htonl((a << 24) | (b << 16) | (c << 8) | d))
#define IP6(a,b,c,d,e,f,g,h) {bpf_htonl(a << 16 | b), bpf_htonl(c << 16 | d), bpf_htonl(e << 16 | f), bpf_htonl(g << 16 | h)}

#define LOCALHOST_IP4 IP4(127, 0, 0, 1)
static const __be32 LOCALHOST_IP6[4] = IP6(0, 0, 0, 0, 0, 0, 0, 1);

#define UNSPEC_IP4 0
static const __be32 UNSPEC_IP6[4] = IP6(0, 0, 0, 0, 0, 0, 0, 0);

// 198.19.248.1
#define HOST_VNET_IP4 IP4(198, 19, 248, 1)
// fd07:b51a:cc66:f0::1
static const __be32 HOST_VNET_IP6[4] = IP6(0xfd07, 0xb51a, 0xcc66, 0x00f0, 0x0000, 0x0000, 0x0000, 0x0001);

#define UDP_BIND_DEBOUNCE 20 // ms

const volatile __u64 config_netns_cookie = 0;
// easier to check this in fentry hook
const volatile __u64 config_cgroup_id = 0;

#define copy4(dst, src) \
    dst[0] = src[0]; \
    dst[1] = src[1]; \
    dst[2] = src[2]; \
    dst[3] = src[3];

struct fwd_meta {
    // UDP notification is delayed until first recvmsg
    bool has_udp_meta;
    bool udp_notify_pending;
};

struct udp_meta {
    struct bpf_timer notify_timer;
};

struct notify_event {
    __u8 dirty_flags;
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
	__uint(map_flags, BPF_F_NO_PREALLOC);
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

static void send_notify(__u8 dirty_flags) {
    bpf_printk("*** notify");
    struct notify_event event = {
        .dirty_flags = dirty_flags,
    };
    int ret = bpf_ringbuf_output(&notify_ring, &event, sizeof(event), 0);
    if (ret != 0) {
        bpf_printk("failed to send notify");
    }
}

static bool cancel_udp_notify(struct fwd_meta *meta, void *ctx) {
    if (meta->udp_notify_pending) {
        __u64 cookie = bpf_get_socket_cookie(ctx);
        // workaround for kernel bug: cancel timer first, then delete, to avoid deadlock
        // between timer_cb deleting self from map, and another thread deleting timer from map and waiting for cancel
        struct udp_meta *udp = bpf_map_lookup_elem(&udp_meta_map, &cookie);
        if (udp != NULL) {
            // WA for deadlock: cancel while not under map lock
            bpf_timer_cancel(&udp->notify_timer);
        }
        bpf_map_delete_elem(&udp_meta_map, &cookie);
        meta->udp_notify_pending = false;
        return true; // canceled
    }

    return false;
}

SEC("cgroup/sock_release")
int pmon_sock_release(struct bpf_sock *sk) {
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
    cancel_udp_notify(meta, sk);
    send_notify(sk->type == SOCK_STREAM ? LTYPE_TCP : LTYPE_UDP);

    return VERDICT_PROCEED;
}

static int udp_timer_cb(void *map, int *key, struct udp_meta *val) {
    bpf_printk("udp debounce fired");
    send_notify(LTYPE_UDP);

    // delete self to clear timer
    int ret = bpf_map_delete_elem(map, key);
    if (ret != 0) {
        bpf_printk("failed to delete udp meta");
    }

    return 0;
}

// returns: whether conditions were met
static bool postbind_common(struct bpf_sock *sk) {
    // only intended netns
    if (!check_netns(sk)) {
        return false;
    }
    // only TCP or UDP
    if (sk->type != SOCK_STREAM && sk->type != SOCK_DGRAM) {
        bpf_printk("not tcp or udp");
        return false;
    }

    // save to map
    struct fwd_meta init_meta = {};
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, sk, &init_meta, BPF_SK_STORAGE_GET_F_CREATE);
    if (meta == NULL) {
        bpf_printk("failed to save meta");
        return true;
    }

    // notify (TCP). UDP delayed until first recvmsg
    if (sk->type == SOCK_STREAM) {
        send_notify(LTYPE_TCP);
    } else {
        meta->udp_notify_pending = true;

        // start timer
        struct udp_meta init_udp = {};
        __u64 cookie = bpf_get_socket_cookie(sk);
        bpf_map_update_elem(&udp_meta_map, &cookie, &init_udp, BPF_ANY);
        struct udp_meta *udp = bpf_map_lookup_elem(&udp_meta_map, &cookie);
        if (udp == NULL) {
            bpf_printk("failed to lookup udp meta");
            return true;
        }

        bpf_timer_init(&udp->notify_timer, &udp_meta_map, CLOCK_MONOTONIC);
        bpf_timer_set_callback(&udp->notify_timer, udp_timer_cb);
        bpf_timer_start(&udp->notify_timer, UDP_BIND_DEBOUNCE * 1000 * 1000, 0);
    }

    return true;
}

static int recvmsg_common(struct bpf_sock_addr *ctx) {
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, NULL, 0);
    if (meta == NULL) {
        return VERDICT_PROCEED;
    }

    // if connect() called as client socket, sk storage will already be deleted
    if (cancel_udp_notify(meta, ctx)) {
        bpf_printk("recvmsg: first udp notify (is server)");
        // if delete failed, timer already fired, so no need to notify again
        send_notify(LTYPE_UDP);
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
    if (cancel_udp_notify(meta, ctx)) {
        bpf_printk("sendmsg: clear pending (is client)");
    }

    return VERDICT_PROCEED;
}

// this handles 2 cases: UDP connect, and TCP bind-before-connect (for explicit client port)
// returns: whether conditions were met
static bool connect_common(struct bpf_sock_addr *ctx) {
    // be careful of connect(AF_UNSPEC)
    struct fwd_meta *meta = bpf_sk_storage_get(&sk_meta_map, ctx->sk, NULL, 0);
    if (meta == NULL) {
        return false;
    }

    // delete timer
    cancel_udp_notify(meta, ctx);

    bpf_sk_storage_delete(&sk_meta_map, ctx->sk);
    return true;
}

/*
 * v4
 */
static bool check_ip4(struct bpf_sock *sk) {
    if (sk->src_ip4 != LOCALHOST_IP4 && sk->src_ip4 != UNSPEC_IP4) {
        bpf_printk("not localhost or unspec %x", bpf_ntohl(sk->src_ip4));
        return false;
    }

    return true;
}

SEC("cgroup/post_bind4")
int pmon_post_bind4(struct bpf_sock *sk) {
    // only localhost
    if (!check_ip4(sk)) {
        return VERDICT_PROCEED;
    }

    if (postbind_common(sk)) {
        bpf_printk("post_bind4: %x:%d", bpf_ntohl(sk->src_ip4), sk->src_port);
    }
    return VERDICT_PROCEED;
}

SEC("cgroup/connect4")
int pmon_connect4(struct bpf_sock_addr *ctx) {
    if (connect_common(ctx)) {
        bpf_printk("connect4: delete sk %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    }
    return VERDICT_PROCEED;
}

SEC("cgroup/recvmsg4")
int pmon_recvmsg4(struct bpf_sock_addr *ctx) {
    bpf_printk("recvmsg4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return recvmsg_common(ctx);
}

SEC("cgroup/sendmsg4")
int pmon_sendmsg4(struct bpf_sock_addr *ctx) {
    bpf_printk("sendmsg4: %x:%d", bpf_ntohl(ctx->user_ip4), bpf_ntohs(ctx->user_port));
    return sendmsg_common(ctx);
}

SEC("cgroup/getpeername4")
int pmon_getpeername4(struct bpf_sock_addr *ctx) {
    // host vnet never makes inbound connections except for port forwards,
    // and it never runs servers. that means the only possibility if getpeername==host is that it's an incoming port forward connection
    // TODO: this is imperfect. we should check ports or at least TCP state == ESTABLISHED/CLOSED. otherwise this could have odd behavior if people are still connecting a socket, e.g. attempted conn to
    // but that's very unlikely and I don't think getpeername works if not connected?

    // rewrite addr if peer is host vnet (198.19.248.1)
    if (ctx->user_ip4 == HOST_VNET_IP4) {
        ctx->user_ip4 = LOCALHOST_IP4;
    }

    return VERDICT_PROCEED;
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
int pmon_post_bind6(struct bpf_sock *sk) {
    // only localhost
    if (!check_ip6(sk)) {
        return VERDICT_PROCEED;
    }

    if (postbind_common(sk)) {
        bpf_printk("post_bind6: %08x%08x%08x%08x:%d", bpf_ntohl(sk->src_ip6[0]), bpf_ntohl(sk->src_ip6[1]), bpf_ntohl(sk->src_ip6[2]), bpf_ntohl(sk->src_ip6[3]), sk->src_port);
    }
    return VERDICT_PROCEED;
}

SEC("cgroup/connect6")
int pmon_connect6(struct bpf_sock_addr *ctx) {
    if (connect_common(ctx)) {
        bpf_printk("connect6: deleted sk %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    }
    return VERDICT_PROCEED;
}

SEC("cgroup/recvmsg6")
int pmon_recvmsg6(struct bpf_sock_addr *ctx) {
    bpf_printk("recvmsg6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return recvmsg_common(ctx);
}

SEC("cgroup/sendmsg6")
int pmon_sendmsg6(struct bpf_sock_addr *ctx) {
    bpf_printk("sendmsg6: %08x%08x%08x%08x:%d", bpf_ntohl(ctx->user_ip6[0]), bpf_ntohl(ctx->user_ip6[1]), bpf_ntohl(ctx->user_ip6[2]), bpf_ntohl(ctx->user_ip6[3]), bpf_ntohs(ctx->user_port));
    return sendmsg_common(ctx);
}

SEC("cgroup/getpeername6")
int pmon_getpeername6(struct bpf_sock_addr *ctx) {
    // host vnet never makes inbound connections except for port forwards,
    // and it never runs servers. that means the only possibility if getpeername==host is that it's an incoming port forward connection
    // TODO: this is imperfect. we should check ports or at least TCP state == ESTABLISHED/CLOSED. otherwise this could have odd behavior if people are still connecting a socket, e.g. attempted conn to
    // but that's very unlikely and I don't think getpeername works if not connected?

    // rewrite addr if peer is host vnet (198.19.248.1)
    if (memcmp(ctx->user_ip6, HOST_VNET_IP6, 16) == 0) {
        copy4(ctx->user_ip6, LOCALHOST_IP6);
    }

    return VERDICT_PROCEED;
}

/*
 * iptables
 *
 * matches NFT_MSG_NEWRULE and NFT_MSG_DELRULE
 * works because docker machine uses iptables-nft
 */

static int nft_change_common() {
    if (bpf_get_current_cgroup_id() != config_cgroup_id) {
        return 0;
    }

    bpf_printk("nft changed");
    send_notify(LTYPE_IPTABLES);
    return 0;
}

// nft_trans_rule_add is generic, but we use fexit to be safe - guaranteed that it's done
SEC("fexit/nf_tables_newrule")
int BPF_PROG(nf_tables_newrule) {
    return nft_change_common();
}

SEC("fexit/nf_tables_delrule")
int BPF_PROG(nf_tables_delrule) {
    return nft_change_common();
}

char _license[] SEC("license") = "GPL";
