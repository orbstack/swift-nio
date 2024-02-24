#include <linux/bpf.h>
#include <linux/errno.h>
#include <linux/fuse.h>
#include <linux/stat.h>
#include <linux/types.h>
#include <stdbool.h>
#include <sys/stat.h>
#include <string.h>
#include "android_fuse.h"

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

#define WORMHOLE_DIR "nix"

SEC("fuse/wormholefs")
int fuse_wormholefs(struct fuse_bpf_args *fa) {
    switch (fa->opcode) {
        // note: we only hook LOOKUP and not READDIR, so /$WORMHOLE doesn't show up in ls unless it was already there in backing
        // this is weird, but technically OK on POSIX FS because it could have been caused by a race between readdir and stat/open every time we access it
        // it's also more seamless and doesn't break anything we're using this for
        case FUSE_LOOKUP | FUSE_PREFILTER: {
            __u64 nodeid = fa->nodeid;
            const char* name = fa->in_args[0].value;
            struct fuse_entry_bpf_out* febo = fa->out_args[1].value;

            // remove bpf from all entries. we do nothing other than /$WORMHOLE lookup
            bpf_printk("fuse_lookup [pre]: nodeid=%llu opcode=%u name=%s\n", fa->nodeid, fa->opcode, name);
            febo->bpf_action = FUSE_ACTION_REMOVE;

            // wormhole dir goes to userspace to replace fd, but needs to go to postfilter first
            // fuse bpf verifier check is garbage - we could panic the kernel
            // so do the size check here (including null terminator)
            if (nodeid == 1 && fa->in_args[0].size == sizeof(WORMHOLE_DIR) && memcmp(name, WORMHOLE_DIR, sizeof(WORMHOLE_DIR)) == 0) {
                // go to userspace
                // doing it in userspace POSTFILTER fails because backing lookup returned ENOENT
                // but attaching to a pure userspace LOOKUP works
                bpf_printk("fuse_lookup [pre]: wormhole\n");
                return 0;
            }

            // everything else goes to backing
            return FUSE_BPF_BACKING;
        }

        default:
            return FUSE_BPF_BACKING;
    }
}

#ifndef DEBUG
char _license[] SEC("license") = "Proprietary";
#else
char _license[] SEC("license") = "GPL";
#endif
