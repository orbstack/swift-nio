#include "asmdefs.h"

#define orig_dst x0
#define src x1
#define count x2
#define dst x3

ENTRY (__memcpy_orb)
    mov dst, orig_dst

// notes:
// - fastest possible memcpy is 270ns for a 16K chunk (57.9 GiB/s). Apple memcpy gets this on 16K pages -- identical perf on host and guest
// - Apple memcpy is ldnp+stnp with q0,q1 (32b/iter)
// - on 4K we can get 300ns (52 GiB/s) with prfm + ldp/stp (64b/iter)
// - little diff between 32b and 64b ldp/stp, but there is a diff
// - ldnp/stnp gets 440 (64b/iter)
// - SIMD is not really faster in any case. according to Apple guide, it's broken down into the same uops
// - arm optimized routines = 630ns (24.8 GiB/s)
// 4k pages: oryon = 630; glibc = 630; apple = 470; arm = 630; simd = 640; simd_nt = 550; oryon-nt-threshold-2048 = 600
// 16k pages: glibc = 590; arm = 590; simd = 610; simd_nt = 400; oryon = 600; oryon-nt-threshold-2048 = 350; apple = 270
.Llarge_loop:
    //prfm pldl1strm, [src, #128]
    ldnp x4, x5, [src]
    stnp x4, x5, [dst]
    ldnp x4, x5, [src, #16]
    stnp x4, x5, [dst, #16]
    ldnp x4, x5, [src, #32]
    stnp x4, x5, [dst, #32]
    ldnp x4, x5, [src, #48]
    stnp x4, x5, [dst, #48]

    add src, src, #64
    add dst, dst, #64
    subs count, count, #64
    b.hi .Llarge_loop

    ret
END (__memcpy_orb)
