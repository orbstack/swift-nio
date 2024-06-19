#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <sys/mman.h>

#include <mach/mach.h>
#include <mach/mach_init.h>
#include <mach/mach_time.h>
#include <mach/mach_vm.h>

uint64_t now_ns() {
    return clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
}

void __check_mach(kern_return_t kr, const char *msg) {
    if (kr != KERN_SUCCESS) {
        mach_error(msg, kr);
        exit(1);
    }
}

void __check_posix(int err, const char *msg) {
    if (err != 0) {
        perror(msg);
        exit(1);
    }
}

#define STRINGIFY(x) #x
#define TOSTRING(x) STRINGIFY(x)
#define CHECK_MACH(kr) __check_mach(kr, "mach error at " __FILE__ ":" TOSTRING(__LINE__))
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

#define TOTAL_BYTES (8ULL * 1024 * 1024 * 1024) // GiB
#define CHUNK_BYTES 16384ULL
// #define CHUNK_BYTES (128ULL * 1024 * 1024) // 128 MiB
// #define CHUNK_BYTES (1ULL * 1024 * 1024) // 1 MiB
// #define CHUNK_BYTES (64ULL * 1024) // 64 KiB

#define NUM_CHUNKS (TOTAL_BYTES / CHUNK_BYTES)
#define NUM_PAGES (TOTAL_BYTES / PAGE_SIZE)

#define for_each_chunk(addr, base_addr) \
    for (mach_vm_address_t addr = base_addr; addr < base_addr + TOTAL_BYTES; addr += CHUNK_BYTES)

#define for_each_page(addr, base_addr) \
    for (mach_vm_address_t addr = base_addr; addr < base_addr + TOTAL_BYTES; addr += PAGE_SIZE)

void touch_all_pages(mach_vm_address_t base_addr) {
    for_each_page(addr, base_addr) {
        *(volatile uint8_t *)addr = 0xaa;
    }
}

void new_entry_chunk_at(mach_port_t task, mach_vm_address_t addr, mach_vm_size_t chunk_size) {
    mach_port_t chunk_port = MACH_PORT_NULL;
    CHECK_MACH(mach_make_memory_entry_64(task, &chunk_size, 0, MAP_MEM_NAMED_CREATE
            | MAP_MEM_LEDGER_TAGGED
            | VM_PROT_READ
            | VM_PROT_WRITE
            | VM_PROT_EXECUTE, &chunk_port, MACH_PORT_NULL));

    CHECK_MACH(mach_vm_map(task, &addr, CHUNK_BYTES, 0, VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE | VM_MAKE_TAG(250), chunk_port, 0, 0, VM_PROT_READ | VM_PROT_WRITE, VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE, VM_INHERIT_NONE));

    CHECK_MACH(mach_port_deallocate(mach_task_self(), chunk_port));
}

void new_purgable_chunk_at(mach_port_t task, mach_vm_address_t addr, mach_vm_size_t chunk_size) {
    CHECK_MACH(mach_vm_allocate(task, &addr, chunk_size, VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE | VM_FLAGS_PURGABLE | VM_MAKE_TAG(250)));
}

int main(int argc, char **argv) {
    mach_port_t host = mach_host_self();
    mach_port_t task = mach_task_self();

    // reserve contig address space
    mach_vm_address_t base_addr = 0;
    // reserve space
    TIME_BLOCK(reserve_space, {
        CHECK_MACH(mach_vm_map(task, &base_addr, TOTAL_BYTES, 0, VM_FLAGS_ANYWHERE | VM_MAKE_TAG(250), 0, 0, 0, VM_PROT_READ | VM_PROT_WRITE, VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE, VM_INHERIT_NONE));
    });

    // map memory in chunks
    TIME_BLOCK_EACH(mach_make_entry_and_map, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            new_entry_chunk_at(task, addr, CHUNK_BYTES);
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
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES, MADV_FREE_REUSABLE));
        }
    });

    // touch all of the memory
    for (int i = 0; i < 3; i++) {
        TIME_BLOCK_EACH(touch_memory, NUM_PAGES, {
            touch_all_pages(base_addr);
        });
    }

    // map memory in chunks
    TIME_BLOCK_EACH(mach_make_entry_and_map, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            new_entry_chunk_at(task, addr, CHUNK_BYTES);
        }
    });

    return 0;
}
