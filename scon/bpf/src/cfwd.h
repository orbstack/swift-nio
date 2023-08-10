// Copyright 2023 Orbital Labs, LLC
// License: proprietary and confidential.

// cfwd: port-80 forwarding for Docker container mDNS
// --------------------------------------------------
// For Docker containers only: if no port 80 listener, redirect incoming TCP conn
// on port 80 to lowest port number listening on 0.0.0.0, if conn is from macOS
// host bridge. Intended for mDNS browser convenience.
//
// Priority ports (prefer if listening): 8080, 3000, 5173, 8000
// Blocked ports (DB): 3306(mysql), 5432(postgres), 6379(redis), 27017(mongo)
//
// simplified: just port scan, no need for listener tracking or per-netns cache

#define CFWD_PORT 80
// max ephemeral port. unlikely that user goes lower
#define CFWD_MAX_SCAN_PORT 32767

// 10.183.233.241
#define NAT64_SRC_IP4 IP4(10, 183, 233, 241)

struct cfwd_host_ip_key {
    __be32 ip6or4[4]; // network byte order
};

struct cfwd_host_ip {
    __u8 unused;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 16); // macOS max = 8 vlan bridges
    __type(key, struct cfwd_host_ip_key);
    __type(value, struct cfwd_host_ip);
} cfwd_host_ips SEC(".maps");

static bool cfwd_try_assign_port(struct bpf_sk_lookup *ctx, int port) {
    struct bpf_sock *sk;
    if (ctx->family == AF_INET6) {
        struct bpf_sock_tuple tuple = {
            .ipv6 = {
                .saddr = {0},
                .sport = 0,
                .dport = bpf_htons(port),
            },
        };
        copy4(tuple.ipv6.daddr, ctx->local_ip6);
        sk = bpf_sk_lookup_tcp(ctx, &tuple, sizeof(tuple.ipv6), BPF_F_CURRENT_NETNS, 0);
    } else {
        struct bpf_sock_tuple tuple = {
            .ipv4 = {
                .saddr = 0,
                .sport = 0,
                .daddr = ctx->local_ip4,
                .dport = bpf_htons(port),
            },
        };
        sk = bpf_sk_lookup_tcp(ctx, &tuple, sizeof(tuple.ipv4), BPF_F_CURRENT_NETNS, 0);
    }
    if (sk == NULL) {
        return false;
    }

    bpf_printk("cfwd: assigning sk to port %u", port);
    int ret = bpf_sk_assign(ctx, sk, 0);
    if (ret != 0) {
        // even if failed, don't try to assign another one, just continue
        bpf_printk("cfwd: failed to assign sk: %d", ret);
    }

    bpf_sk_release(sk);
    return true;
}

struct cfwd_scan_ctx {
    struct bpf_sk_lookup *ctx;
};

static int cfwd_loop_cb(__u32 index, struct cfwd_scan_ctx *ctx) {
    __u16 port = index + 1; // no port 0
    if (port == 3306 || port == 5432 || port == 6379 || port == 27017) {
        return 0; // continue
    }

    // try this port
    if (cfwd_try_assign_port(ctx->ctx, port)) {
        bpf_printk("cfwd: found suitable listener on port %u", port);
        return 1; // stop
    }

    return 0; // continue
}

// this is per-netns, so no need to check netns cookie
SEC("sk_lookup/") // cilium/ebpf incorrectly expects trailing "/", libbpf doesn't
int cfwd_sk_lookup(struct bpf_sk_lookup *ctx) {
    bpf_printk("cfwd: sk_lookup: family=%u protocol=%u local_port=%u", ctx->family, ctx->protocol, ctx->local_port);
    // only IPv4/v6, TCP, port 80, + container netns&cgroup
    if ((ctx->family != AF_INET && ctx->family != AF_INET6) ||
            ctx->protocol != IPPROTO_TCP || ctx->local_port != CFWD_PORT) {
        return SK_PASS;
    }

    // if there's already a port 80 listener, skip (fastpath)
    if (cfwd_try_assign_port(ctx, CFWD_PORT)) return SK_PASS;

    // verify src addr: must be macOS host bridge IP. works b/c it's over bridge, not NAT
    // and a special case for NAT64 source IP. see bnat for why we need this weird IP
    if (!(ctx->family == AF_INET && ctx->remote_ip4 == NAT64_SRC_IP4)) {
        struct cfwd_host_ip_key host_ip_key;
        if (ctx->family == AF_INET) {
            // make 4-in-6 mapped IP
            host_ip_key.ip6or4[0] = 0;
            host_ip_key.ip6or4[1] = 0;
            host_ip_key.ip6or4[2] = bpf_htonl(0xffff);
            host_ip_key.ip6or4[3] = ctx->remote_ip4;
        } else {
            memcpy(host_ip_key.ip6or4, ctx->remote_ip6, 16);
        }
        struct cfwd_host_ip *host_ip = bpf_map_lookup_elem(&cfwd_host_ips, &host_ip_key);
        if (host_ip == NULL) {
            bpf_printk("cfwd: not mac host bridge IP: %08x%08x%08x%08x", bpf_ntohl(host_ip_key.ip6or4[0]), bpf_ntohl(host_ip_key.ip6or4[1]), bpf_ntohl(host_ip_key.ip6or4[2]), bpf_ntohl(host_ip_key.ip6or4[3]));
            return SK_PASS;
        }
    }

    // all verified: we want to redirect this connection if there's a suitable target.
    // first try priority ports, for perf (avoid scanning) and consistent behavior
    if (cfwd_try_assign_port(ctx, 8080)) return SK_PASS;
    if (cfwd_try_assign_port(ctx, 3000)) return SK_PASS;
    if (cfwd_try_assign_port(ctx, 5173)) return SK_PASS;
    if (cfwd_try_assign_port(ctx, 8000)) return SK_PASS;

    struct cfwd_scan_ctx scan_ctx = {
        .ctx = ctx,
    };
    // -1 because there's no port 0
    // scanning 32767 ports takes ~1.25 ms, 2.5 ms for 65535. it's fast enough, much simpler than caching
    int ret = bpf_loop(CFWD_MAX_SCAN_PORT-1, cfwd_loop_cb, &scan_ctx, 0);
    if (ret < 0) {
        bpf_printk("cfwd: failed to scan for suitable listener: %d", ret);
        return SK_PASS;
    }

    return SK_PASS;
}
