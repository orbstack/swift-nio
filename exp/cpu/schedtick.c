#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <stdbool.h>
#include <stdint.h>

#define NSEC_PER_SEC 1000000000ULL

#define RUN_SECS 10ULL

static inline uint64_t now_ticks()
{
    // read cntvct_el0
    uint64_t res;
    asm volatile("mrs %0, cntvctss_el0" : "=r"(res));
    return res;
}

static inline uint64_t cntfrq()
{
    // read cntfrq_el0
    uint64_t res;
    asm volatile("mrs %0, cntfrq_el0" : "=r"(res));
    return res;
}

static inline uint64_t to_ns(uint64_t ticks)
{
    return ticks * NSEC_PER_SEC / cntfrq();
}

static inline uint64_t to_ticks(uint64_t ns)
{
    return ns * cntfrq() / NSEC_PER_SEC;
}

__attribute__((noinline)) static void busy_loop(uint64_t nsec)
{
    uint64_t start = now_ticks();
    uint64_t end_time = start + to_ticks(nsec);
    while (now_ticks() < end_time)
    {
        // do nothing
    }
}

int main()
{
    uint64_t start = now_ticks();
    uint64_t last = start;
    uint64_t gaps = 0;
    uint16_t total_gap_ticks = 0;
    uint64_t end_time = start + to_ticks(RUN_SECS * NSEC_PER_SEC);

    printf("cntfrq %llu\n", cntfrq());

    // burn cpu for 1s to ramp frequency
    busy_loop(1 * NSEC_PER_SEC);

    while (true)
    {
        // the CPU runs much faster than the cntfrq, so if we're never scheduled out it should always increase by +1
        uint64_t now = now_ticks();
        uint64_t diff = now - last;
        /*
        critical section:
 690:	aa0103e0 	mov	x0, x1
 694:	d53be0c1 	mrs	x1, cntvctss_el0
 698:	cb000020 	sub	x0, x1, x0
 69c:	f100081f 	cmp	x0, #0x2
 6a0:	54ffff89 	b.ls	690 <main+0x50>  // b.plast
        */
        if (__builtin_expect(diff > 2, 0)) // 83ns
        {
            // a gap occurred
            // printf("Gap: %llu\n", diff);
            gaps++;
            total_gap_ticks += diff;
            if (now >= end_time)
            {
                break;
            }
        }
        // we're equally likely to get interrupted at any of the 5 critical section instructions
        // so we need to use the last cntvct reading
        // gap handling should always be <2 ticks worth of cycles if we're not interrupted
        last = now;
    }

    end_time = now_ticks();
    uint64_t total_time_ns = to_ns(end_time - start);
    double total_time_secs = (double)total_time_ns / (double)NSEC_PER_SEC;
    uint64_t total_gaps_ns = to_ns(total_gap_ticks);
    printf("# gaps: %llu (%.1f/s)\n", gaps, (double)gaps / total_time_secs);
    printf("Total gap time: %llu us (%.3f%%)\n", total_gaps_ns / 1000, (double)total_gaps_ns / total_time_ns * 100);
    printf("avg %llu ns/gap\n", total_gaps_ns / gaps);
    printf("Total time: %.1f s\n", total_time_secs);
    return 0;
}
