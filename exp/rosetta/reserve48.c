#define _GNU_SOURCE

#include <time.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/mman.h>
#include <stdint.h>
#include <errno.h>

#define START_ADDR (1ULL << 47)
#define END_ADDR ((~0ULL) >> (64 - 47 - 1))

const uint64_t try_sizes[] = {
    64ULL * 1024 * 1024 * 1024,   // 64 GiB
    32ULL * 1024 * 1024 * 1024,   // 32 GiB
    16ULL * 1024 * 1024 * 1024,   // 16 GiB
    4ULL * 1024 * 1024 * 1024,    // 4 GiB
    1ULL * 1024 * 1024 * 1024, // 1 GiB
    512ULL * 1024 * 1024,      // 512 MiB
    128ULL * 1024 * 1024,      // 128 MiB
    32ULL * 1024 * 1024,       // 32 MiB
    1ULL * 1024 * 1024,        // 1 MiB
    4096ULL                    // 1 page
};

#define min(a, b) ((a) < (b) ? (a) : (b))

#define NUM_SIZES (sizeof(try_sizes) / sizeof(try_sizes[0]))

int main() {
    printf("START_ADDR: %llx, END_ADDR: %llx\n", START_ADDR, END_ADDR);

    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);

    for (uint64_t addr = START_ADDR; addr < END_ADDR; ) {
        uint64_t rem = END_ADDR - addr;
        for (size_t i = 0; i < NUM_SIZES; i++) {
            uint64_t size = min(try_sizes[i], rem);
            if (size == 0) {
                break;
            }
            // printf("addr: %llx, size: %llx\n", addr, size);

            void *p = mmap((void *)addr, size, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE | MAP_NORESERVE, -1, 0);
            if (p == MAP_FAILED) {
                if (errno == EEXIST) {
                    // if error is EEXIST, we ran into an existing mapping
                    // if we're at the last size (4096), that means something's already here, so skip it (fallthrough to addr += size)
                    // if not, then try the next size (continue)
                    // TODO: what happens if the "something here" is glibc stuff and it later gets unmapped? hopefully that doesn't happen...
                    if (i != NUM_SIZES - 1)  {
                        continue;
                    }
                } else {
                    // if error isn't EEXIST (e.g. due to rlimit), something's wrong and we won't be able to reserve all of the memory
                    perror("init mmap");
                    exit(1);
                }
            }

            addr += size;
            break;
        }
    }

    clock_gettime(CLOCK_MONOTONIC, &end);
    uint64_t duration = (end.tv_sec - start.tv_sec) * 1000000000 + (end.tv_nsec - start.tv_nsec);
    printf("Duration: %lu us\n", duration / 1000);

    // verify    
    void *p = mmap(NULL, 4096, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (p == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }
    printf("p: %p\n", p);

    return 0;
}
