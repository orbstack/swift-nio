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
    int start;
    struct bpf_sk_lookup *ctx;
};

static int cfwd_loop_cb(__u32 index, struct cfwd_scan_ctx *ctx) {
    __u16 port = ctx->start + index;
    if (port == 3306) return 0; // mysql
    if (port == 5432) return 0; // postgres
    if (port == 6379) return 0; // redis
    if (port == 8443) return 0; // https (we don't currently support HTTPS - this is only for port 80)
    if (port == 27017) return 0; // mongo

    // try this port
    if (cfwd_try_assign_port(ctx->ctx, port)) {
        bpf_printk("cfwd: found suitable listener on port %u", port);
        return 1; // break
    }

    return 0; // continue
}

// start inclusive, end exclusive
static bool cfwd_try_port_range(struct bpf_sk_lookup *ctx, int start, int end) {
    struct cfwd_scan_ctx scan_ctx = {
        .start = start,
        .ctx = ctx,
    };

    // -1 because there's no port 0
    // scanning 32767 ports takes ~1.25 ms, 2.5 ms for 65535. it's fast enough, much simpler than caching
    int ret = bpf_loop(end - start, cfwd_loop_cb, &scan_ctx, 0);
    if (ret < 0) {
        bpf_printk("cfwd: scan loop failed: %d", ret);
        return false;
    }

    return ctx->sk != NULL;
}

// verify src addr: must be macOS host bridge IP. works b/c it's over bridge, not NAT
// and a special case for NAT64 source IP. see bnat for why we need this weird IP
static bool cfwd_should_redirect_for_ip(struct bpf_sk_lookup *ctx) {
    // NAT64 for mDNS
    if (ctx->family == AF_INET && ctx->remote_ip4 == NAT64_SRC_IP4) {
        return true;
    }

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
    return host_ip != NULL;
}

// this is per-netns, so no need to check netns cookie
SEC("sk_lookup/") // cilium/ebpf incorrectly expects trailing "/", libbpf doesn't
int cfwd_sk_lookup(struct bpf_sk_lookup *ctx) {
    bpf_printk("cfwd: sk_lookup: family=%u protocol=%u local_port=%u", ctx->family, ctx->protocol, ctx->local_port);
    // only IPv4/v6, TCP, port 80, + container netns&cgroup
    if ((ctx->family != AF_INET && ctx->family != AF_INET6) || ctx->protocol != IPPROTO_TCP) {
        return SK_PASS;
    }

    if (ctx->local_port == 80) {
        // fastpath: if there's a real listener, or not from macOS
        if (cfwd_try_assign_port(ctx, ctx->local_port)) return SK_PASS;
        if (!cfwd_should_redirect_for_ip(ctx)) return SK_PASS;

        // all verified: we want to redirect this connection if there's a suitable target.
        // first try priority ports, for perf (avoid scanning) and consistent behavior
        if (cfwd_try_assign_port(ctx, 8080)) return SK_PASS; // common
        if (cfwd_try_assign_port(ctx, 3000)) return SK_PASS; // nodejs common
        if (cfwd_try_assign_port(ctx, 5173)) return SK_PASS; // vite
        if (cfwd_try_assign_port(ctx, 8000)) return SK_PASS; // python common

        // now try ranges
        // 8000-9000: most likely to be used for HTTP ports. (we've already checked 80 and other common ports)
        if (cfwd_try_port_range(ctx, 8000, 9000)) return SK_PASS;
        // 81-8000: try the lower half next. (start at 81 b/c we've already checked 80)
        // lower ports are all SSH, telnet, mail, etc.
        if (cfwd_try_port_range(ctx, 81, 8000)) return SK_PASS;
        // 9000-32767: try the upper half next. (start at 9000 b/c we've already checked 8000)
        if (cfwd_try_port_range(ctx, 9000, 32767+1 /*inclusive*/)) return SK_PASS;
    } else if (ctx->local_port == 443) {
        // fastpath: if there's a real listener, or not from macOS
        if (cfwd_try_assign_port(ctx, ctx->local_port)) return SK_PASS;
        if (!cfwd_should_redirect_for_ip(ctx)) return SK_PASS;

        // try 8443
        if (cfwd_try_assign_port(ctx, 8443)) return SK_PASS;
    } else {
        // not a port we care about
        return SK_PASS;
    }

    // not found
    bpf_printk("cfwd: no suitable listener found");
    return SK_PASS;
}
