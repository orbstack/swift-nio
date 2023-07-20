// Copyright 2023 Orbital Labs, LLC
// License: proprietary and confidential.

// Filter sysctl

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

#define PANIC_TIMEOUT "kernel/panic"

SEC("cgroup/sysctl")
int sysctl_filter(struct bpf_sysctl *ctx) {
	if (!ctx->write) {
        bpf_printk("sysctl: read\n");
        return VERDICT_PROCEED;
    }

    char buf[256] = {0};
    int ret = bpf_sysctl_get_name(ctx, buf, sizeof(buf), 0);
    if (ret < 0) {
        bpf_printk("sysctl: get name failed: %d\n", ret);
        return VERDICT_PROCEED;
    }
    bpf_printk("sysctl: write %s\n", buf);

    if (memcmp(buf, PANIC_TIMEOUT, sizeof(PANIC_TIMEOUT)) != 0) {
        bpf_printk("sysctl: not panic timeout\n");
        return VERDICT_PROCEED;
    }

    ret = bpf_sysctl_set_new_value(ctx, "-1", 3);
    if (ret < 0) {
        bpf_printk("sysctl: set new value failed: %d\n", ret);
        return VERDICT_PROCEED;
    }

    bpf_printk("sysctl: set new value succeeded\n");
    return VERDICT_PROCEED;
}

#ifdef DEBUG
char _license[] SEC("license") = "GPL";
#else
char _license[] SEC("license") = "Proprietary";
#endif
