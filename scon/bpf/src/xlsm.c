// Copyright 2024 Orbital Labs, LLC
// License: GPL (careful!)

#include <linux/types.h>
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>
#include <stdint.h>
#include <linux/errno.h>
#include <linux/bpf.h>

//#define DEBUG

#ifndef DEBUG
#ifdef bpf_printk
#undef bpf_printk
#endif
#define bpf_printk(fmt, ...) \
    do { \
    } while (0)
#endif

/*
 * hook for bpf(2) syscall
 *
 * This prevents systemd from loading its "restrict_filesystems" eBPF LSM program.
 * The restrict_filesystems program hooks all open() syscalls and adds significant performance overhead, becoming responsible for *most* of the runtime of open() in the kernel.
 * To make things worse, every machine running a modern version of systemd will load one copy of this program, causing overhead to accumulate and affect the performance of all machines (including Docker and ovm/scon).
 */
SEC("lsm/bpf")
int BPF_PROG(xlsm_bpf, int cmd, union bpf_attr *attr, unsigned int size, int ret) {
    if (ret) return ret;

    if (cmd == BPF_PROG_LOAD && attr->prog_type == BPF_PROG_TYPE_LSM) {
        if (__builtin_memcmp(attr->prog_name, "restrict_filesy", BPF_OBJ_NAME_LEN) == 0) {
            bpf_printk("xlsm: blocking restrict_filesystems LSM program");
            return -EPERM;
        }
    }

    return -0;
}

// required for BPF LSM
char __license[] SEC("license") = "GPL";
