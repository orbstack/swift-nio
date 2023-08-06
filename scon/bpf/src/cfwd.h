// Copyright 2023 Orbital Labs, LLC
// License: proprietary and confidential.

// cfwd: port-80 forwarding for Docker container mDNS
// --------------------------------------------------
// For Docker containers only: if no port 80 listener, redirect incoming TCP conn
// on port 80 to lowest port number listening on 0.0.0.0, if conn is from macOS
// host bridge. Intended for mDNS browser convenience.
//
// Priority ports (prefer if listening): 3000, 5173, 8000, 8080
// Blocked ports (DB): 3306(mysql), 5432(postgres), 6379(redis), 27017(mongo)
//
// simplified: only tracks TCP listeners, only acts on port 80, only containers, no GPL timer funcs

#define CFWD_PORT 80

#define UNSPEC_IP4 0
static const __be32 UNSPEC_IP6[4] = IP6(0, 0, 0, 0, 0, 0, 0, 0);

const volatile __u64 config_docker_cgroup_id = 0;

struct cfwd_listener_key {
    __u64 netns_cookie;
    __u16 port; // host byte order
    bool is_ip6;
};

struct cfwd_listener {
    __u8 unused;
};

struct cfwd_host_ip_key {
    __be32 ip6or4[4]; // network byte order
};

struct cfwd_host_ip {
    __u8 unused;
};

struct cfwd_sk_meta {
    __u8 unused;
};

struct {
    // HASH_OF_MAPS (map[netns_cookie][port]) might be nice but can only be updated from userspace
    __uint(type, BPF_MAP_TYPE_SOCKHASH);
    //__uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 16384);
    __type(key, struct cfwd_listener_key);
    __type(value, __u64);
} cfwd_listener_socks_map SEC(".maps");

// need this b/c we can't iterate through sockhash
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 16384);
    __type(key, struct cfwd_listener_key);
    __type(value, struct cfwd_listener);
} cfwd_listener_keys_map SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __uint(max_entries, 16);
    __type(key, struct cfwd_host_ip_key);
    __type(value, struct cfwd_host_ip);
} cfwd_host_ips_map SEC(".maps");

// sk storage to flag sockets as stored
struct {
	__uint(type, BPF_MAP_TYPE_SK_STORAGE);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, int);
	__type(value, struct cfwd_sk_meta);
} cfwd_sk_meta_map SEC(".maps");

static bool cfwd_check_ns(void *ctx) {
    __u64 cur_netns = bpf_get_netns_cookie(ctx);
    if (config_netns_cookie == cur_netns) {
        bpf_printk("cfwd: skipping docker machine netns %llu", cur_netns);
        return false;
    }

    // check cgroup: direct child of docker
    // 0 = root, 1 = docker, 2 = container
    __u64 cur_cgroup = bpf_get_current_cgroup_id();
    __u64 l1_cgroup = bpf_get_current_ancestor_cgroup_id(1);
    __u64 l2_cgroup = bpf_get_current_ancestor_cgroup_id(2);
    if (l1_cgroup != config_docker_cgroup_id || l2_cgroup != cur_cgroup) {
        bpf_printk("cfwd: skipping non-docker container cgroup cur=%llu l1=%llu l2=%llu", cur_cgroup, l1_cgroup, l2_cgroup);
        return false;
    }

    return true;
}

static bool cfwd_check_ip4(struct bpf_sock *sk) {
    if (sk->src_ip4 != bpf_htonl(UNSPEC_IP4)) {
        bpf_printk("cfwd: not unspec %x", bpf_ntohl(sk->src_ip4));
        return false;
    }

    return true;
}

SEC("cgroup/sock_release")
int cfwd_sock_release(struct bpf_sock *sk) {
    // only if tracked
    // no need to delete; it's done automatically by kernel on release
    struct cfwd_sk_meta *meta = bpf_sk_storage_get(&cfwd_sk_meta_map, sk, NULL, 0);
    if (meta == NULL) {
        return VERDICT_PROCEED;
    }

    // delete from maps
    struct cfwd_listener_key listener_key = {
        .netns_cookie = bpf_get_netns_cookie(sk),
        .port = bpf_ntohs(sk->dst_port),
        .is_ip6 = (sk->family == AF_INET6),
    };
    bpf_printk("cfwd: deleting listener netns=%llu port=%u is_ip6=%d", listener_key.netns_cookie, listener_key.port, listener_key.is_ip6);
    bpf_map_delete_elem(&cfwd_listener_socks_map, &listener_key);
    bpf_map_delete_elem(&cfwd_listener_keys_map, &listener_key);

    return VERDICT_PROCEED;
}

int cfwd_connect4(struct bpf_sock_addr *ctx) {
    // connect means TCP bind-before-connect, so dissolve the listener
    return cfwd_sock_release(ctx->sk);
}

SEC("cgroup/bind4")
int cfwd_bind4(struct bpf_sock_addr *ctx) {
    if (!cfwd_check_ip4(ctx->sk)) {
        return VERDICT_PROCEED;
    }
    if (!cfwd_check_ns(ctx)) {
        return VERDICT_PROCEED;
    }

    // insert into maps
    struct cfwd_listener_key listener_key = {
        .netns_cookie = bpf_get_netns_cookie(ctx),
        .port = bpf_ntohs(ctx->sk->dst_port),
        .is_ip6 = false,
    };
    struct cfwd_listener listener = {};
    bpf_map_update_elem(&cfwd_listener_socks_map, &listener_key, bpf_sk_fullsock(ctx->sk), BPF_ANY);
    bpf_map_update_elem(&cfwd_listener_keys_map, &listener_key, &listener, BPF_ANY);

    // mark socket as tracked
    struct cfwd_sk_meta meta = {};
    bpf_sk_storage_get(&cfwd_sk_meta_map, ctx->sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);

    return VERDICT_PROCEED;
}

static bool cfwd_check_ip6(struct bpf_sock *sk) {
    if (memcmp(sk->src_ip6, UNSPEC_IP6, 16) != 0) {
        bpf_printk("cfwd: not unspec %08x%08x%08x%08x", bpf_ntohl(sk->src_ip6[0]), bpf_ntohl(sk->src_ip6[1]), bpf_ntohl(sk->src_ip6[2]), bpf_ntohl(sk->src_ip6[3]));
        return false;
    }

    return true;
}

int cfwd_connect6(struct bpf_sock_addr *ctx) {
    // connect means TCP bind-before-connect, so dissolve the listener
    return cfwd_sock_release(ctx->sk);
}

SEC("cgroup/post_bind6")
int cfwd_post_bind6(struct bpf_sock *sk) {
    if (!cfwd_check_ip4(sk)) {
        return VERDICT_PROCEED;
    }
    if (!cfwd_check_ns(sk)) {
        return VERDICT_PROCEED;
    }

    // insert into maps
    struct cfwd_listener_key listener_key = {
        .netns_cookie = bpf_get_netns_cookie(sk),
        .port = bpf_ntohs(sk->dst_port),
        .is_ip6 = true,
    };
    struct cfwd_listener listener = {};
    bpf_map_update_elem(&cfwd_listener_socks_map, &listener_key, sk, BPF_ANY);
    bpf_map_update_elem(&cfwd_listener_keys_map, &listener_key, &listener, BPF_ANY);

    // mark socket as tracked
    struct cfwd_sk_meta meta = {};
    bpf_sk_storage_get(&cfwd_sk_meta_map, sk, &meta, BPF_SK_STORAGE_GET_F_CREATE);

    return VERDICT_PROCEED;
}

static bool cfwd_try_assign_port(struct bpf_sk_lookup *ctx, struct cfwd_listener_key *key, int port) {
    key->port = port;
    struct sock *sk = bpf_map_lookup_elem(&cfwd_listener_socks_map, key);
    if (sk == NULL) {
        bpf_printk("cfwd: no sk for port %u", port);
        return false;
    }

    bpf_printk("cfwd: assigning sk for port %u", port);
    int ret = bpf_sk_assign(ctx, sk, 0);
    if (ret != 0) {
        bpf_printk("cfwd: failed to assign sk for port %u", port);
        return true; // still, having something that failed should prevent us from assigning another one
    }

    return true;
}

struct cfwd_listener_search_ctx {
    __u64 want_netns_cookie;
    bool want_is_ip6;
    __u16 last_found_port;
    struct cfwd_listener_key *found_key;
};

// goal: find min port that's not block
static int cfwd_listener_search_cb(void *map, struct cfwd_listener_key *key, struct cfwd_listener *value, struct cfwd_listener_search_ctx *ctx) {
    bpf_printk("cfwd: search: checking listener netns=%llu port=%u is_ip6=%d", key->netns_cookie, key->port, key->is_ip6);
    if (key->netns_cookie != ctx->want_netns_cookie) {
        bpf_printk("cfwd: search: skipping non-matching: netns");
        return 0; // continue
    }
    if (key->is_ip6 != ctx->want_is_ip6) {
        bpf_printk("cfwd: search: skipping non-matching: ip6");
        return 0; // continue
    }
    // skip blocked
    if (key->port == 3306 || key->port == 5432 || key->port == 6379 || key->port == 27017) {
        bpf_printk("cfwd: search: skipping blocked port %u", key->port);
        return 0; // continue
    }

    // <= because our initial max is 65535
    if (key->port <= ctx->last_found_port) {
        // lower than last
        bpf_printk("cfwd: search: using lower port %u", key->port);
        ctx->last_found_port = key->port;
        ctx->found_key = key;
    }

    // continue
    return 0;
}

// this is per-netns
SEC("sk_lookup")
int cfwd_sk_lookup(struct bpf_sk_lookup *ctx) {
    // only IPv4/v6, TCP, port 80, + container netns&cgroup
    if ((ctx->family != AF_INET && ctx->family != AF_INET6) ||
            ctx->protocol != IPPROTO_TCP || ctx->local_port != CFWD_PORT) {
        return SK_PASS;
    }
    if (!cfwd_check_ns(ctx)) {
        return SK_PASS;
    }

    // if there's already a port 80 listener, skip
    struct cfwd_listener_key listener_key = {
        .netns_cookie = bpf_get_netns_cookie(ctx),
        .is_ip6 = (ctx->family == AF_INET6),
    };
    if (cfwd_try_assign_port(ctx, &listener_key, CFWD_PORT)) {
        bpf_printk("cfwd: already listening on port %u", ctx->local_port);
        return SK_PASS;
    }

    // verify src addr: must be macOS host bridge IP. works b/c it's over bridge, not NAT
    struct cfwd_host_ip_key host_ip_key = {};
    if (ctx->family == AF_INET) {
        // make 4-in-6 mapped IP
        host_ip_key.ip6or4[0] = 0;
        host_ip_key.ip6or4[1] = 0;
        host_ip_key.ip6or4[2] = bpf_htonl(0xffff);
        host_ip_key.ip6or4[3] = ctx->local_ip4;
    } else {
        memcpy(host_ip_key.ip6or4, ctx->local_ip6, 16);
    }
    struct cfwd_host_ip *host_ip = bpf_map_lookup_elem(&cfwd_host_ips_map, &host_ip_key);
    if (host_ip == NULL) {
        bpf_printk("cfwd: not mac host bridge IP");
        return SK_PASS;
    }

    // all verified: we want to redirect this connection if there's a suitable target.
    // first, try to look for priority ports
    // both for reliability (expected behavior in most cases) and for perf (avoid iterating)
    if (cfwd_try_assign_port(ctx, &listener_key, 3000)) return SK_PASS;
    if (cfwd_try_assign_port(ctx, &listener_key, 5173)) return SK_PASS;
    if (cfwd_try_assign_port(ctx, &listener_key, 8000)) return SK_PASS;
    if (cfwd_try_assign_port(ctx, &listener_key, 8080)) return SK_PASS;

    // failed. iterate through cfwd_listener_keys_map and respect blocked ports
    struct cfwd_listener_search_ctx search_ctx = {
        .want_netns_cookie = bpf_get_netns_cookie(ctx),
        .want_is_ip6 = (ctx->family == AF_INET6),
        .last_found_port = 65535,
        .found_key = NULL,
    };
    int ret = bpf_for_each_map_elem(&cfwd_listener_keys_map, cfwd_listener_search_cb, &search_ctx, 0);
    if (ret < 0) {
        bpf_printk("cfwd: failed to iterate through listener keys");
        return SK_PASS;
    }
    if (search_ctx.found_key == NULL) {
        bpf_printk("cfwd: no suitable listener found");
        return SK_PASS;
    }

    // found a suitable listener. let's try to look up in sockhash and assign it
    if (!cfwd_try_assign_port(ctx, search_ctx.found_key, search_ctx.found_key->port)) {
        bpf_printk("cfwd: failed to assign suitable listener");
        return SK_PASS;
    }

    return SK_PASS;
}
