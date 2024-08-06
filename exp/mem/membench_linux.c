#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <sys/mman.h>
#include <stdint.h>

#define PAGE_SIZE 4096

uint64_t now_ns() {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return ts.tv_sec * 1000000000 + ts.tv_nsec;
}

void __check_posix(int err, const char *msg) {
    if (err != 0) {
        perror(msg);
        exit(1);
    }
}

#define STRINGIFY(x) #x
#define TOSTRING(x) STRINGIFY(x)
#define CHECK_POSIX(err) __check_posix(err, "posix error at " __FILE__ ":" TOSTRING(__LINE__))

#define TIME_BLOCK(name, block) \
    { \
        uint64_t name##_start = now_ns(); \
        block \
        uint64_t name##_end = now_ns(); \
        printf(#name ": %llu us\n", (name##_end - name##_start) / 1000); \
    }

#define TIME_BLOCK_EACH(name, count, block) \
    { \
        uint64_t name##_start = now_ns(); \
        block \
        uint64_t name##_end = now_ns(); \
        printf(#name ": %llu us  (each: %llu ns)\n", (name##_end - name##_start) / 1000, (name##_end - name##_start) / count); \
    }

#define TOTAL_BYTES (1ULL * 1024 * 1024 * 1024) // GiB
#define CHUNK_BYTES 16384ULL
// #define CHUNK_BYTES (128ULL * 1024 * 1024) // 128 MiB
// #define CHUNK_BYTES (2ULL * 1024 * 1024) // 2 MiB
// #define CHUNK_BYTES (64ULL * 1024) // 64 KiB

#define NUM_CHUNKS (TOTAL_BYTES / CHUNK_BYTES)
#define NUM_PAGES (TOTAL_BYTES / PAGE_SIZE)

#define for_each_chunk(addr, base_addr) \
    for (uintptr_t addr = base_addr; addr < base_addr + TOTAL_BYTES; addr += CHUNK_BYTES)

#define for_each_page(addr, base_addr) \
    for (uintptr_t addr = base_addr; addr < base_addr + TOTAL_BYTES; addr += PAGE_SIZE)

void touch_all_pages(uintptr_t base_addr) {
    for_each_page(addr, base_addr) {
        *(volatile uint8_t *)addr = 0xaa;
    }
}

void new_purgable_chunk_at(uintptr_t addr, size_t chunk_size) {
    void *ret = mmap((void*)addr, chunk_size, PROT_READ | PROT_WRITE, MAP_ANONYMOUS | MAP_PRIVATE | MAP_FIXED, -1, 0);
    if (ret == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }
}

extern void *__memcpy_aarch64(void *, const void *, size_t);
extern void *__memcpy_aarch64_simd(void *, const void *, size_t);
extern void *__memcpy_aarch64_simd_nt(void *, const void *, size_t);
extern void *__memcpy_apple(void *, const void *, size_t);
extern void *__memcpy_oryon1(void *, const void *, size_t);
extern void *__memcpy_orb(void *, const void *, size_t);

static int memvcmp(void *memory, unsigned char val, unsigned int size)
{
    unsigned char *mm = (unsigned char*)memory;
    return (*mm == val) && memcmp(mm, mm + 1, size - 1) == 0;
}

void memcpy_nonzero_pages(char *dst, char *src, size_t size) {
    for (char *srcp = src; srcp < src + size; srcp += PAGE_SIZE) {
        char *dstp = dst + (srcp - src);
        if (!memvcmp(srcp, 0, PAGE_SIZE)) {
            memcpy(dstp, srcp, PAGE_SIZE);
        }
    }
}

int main(int argc, char **argv) {
    // reserve contig address space
    uintptr_t base_addr = 0;
    // reserve space
    TIME_BLOCK(reserve_space, {
        base_addr = (uintptr_t)mmap(NULL, TOTAL_BYTES, PROT_READ | PROT_WRITE, MAP_ANONYMOUS | MAP_PRIVATE, -1, 0);
        if (base_addr == MAP_FAILED) {
            perror("mmap");
            exit(1);
        }
    });

    // map memory in chunks
    TIME_BLOCK_EACH(mach_make_entry_and_map, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            new_purgable_chunk_at(addr, CHUNK_BYTES);
        }
    });

    // touch all of the memory
    for (int i = 0; i < 3; i++) {
        TIME_BLOCK_EACH(touch_memory, NUM_PAGES, {
            touch_all_pages(base_addr);
        });
    }

    // madvise(REUSABLE) for all
    TIME_BLOCK_EACH(madvise_reusable, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES, MADV_FREE));
        }
    });

    // touch all of the memory
    for (int i = 0; i < 3; i++) {
        TIME_BLOCK_EACH(touch_memory, NUM_PAGES, {
            touch_all_pages(base_addr);
        });
    }

    volatile char *target_buf = mmap(NULL, TOTAL_BYTES, PROT_READ | PROT_WRITE, MAP_ANONYMOUS | MAP_PRIVATE, -1, 0);
    if (target_buf == NULL) {
        perror("malloc");
        exit(1);
    }
    for (int i = 0; i < 100; i++) {
        TIME_BLOCK_EACH(memcpy_chunk, NUM_CHUNKS, {
            for_each_chunk(addr, base_addr) {
                volatile char *target = target_buf + (addr - base_addr);
                __memcpy_orb(target, (void *)addr, CHUNK_BYTES);
                *(volatile uint8_t *)target;
            }
        });
    };

    // sleep(100000);

    return 0;
}
