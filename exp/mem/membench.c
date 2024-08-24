#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>
#include <sys/mman.h>
#include <Hypervisor/Hypervisor.h>

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

void __check_hv(hv_return_t hv, const char *msg) {
    if (hv != HV_SUCCESS) {
        fprintf(stderr, "%s: %d\n", msg, hv);
        exit(1);
    }
}

#define STRINGIFY(x) #x
#define TOSTRING(x) STRINGIFY(x)
#define CHECK_MACH(kr) __check_mach(kr, "mach error at " __FILE__ ":" TOSTRING(__LINE__))
#define CHECK_POSIX(err) __check_posix(err, "posix error at " __FILE__ ":" TOSTRING(__LINE__))
#define CHECK_HV(hv) __check_hv(hv, "hypervisor error at " __FILE__ ":" TOSTRING(__LINE__))

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
// #define CHUNK_BYTES 16384ULL
// #define CHUNK_BYTES (128ULL * 1024 * 1024) // 128 MiB
#define CHUNK_BYTES (4ULL * 1024 * 1024) // 4 MiB
// #define CHUNK_BYTES (64ULL * 1024) // 64 KiB

#define NUM_CHUNKS (TOTAL_BYTES / CHUNK_BYTES)
#define NUM_PAGES (TOTAL_BYTES / PAGE_SIZE)

#define for_each_chunk(addr, base_addr) \
    for (mach_vm_address_t addr = base_addr; addr < base_addr + TOTAL_BYTES; addr += CHUNK_BYTES)

#define for_each_page(addr, base_addr) \
    for (mach_vm_address_t addr = base_addr; addr < base_addr + TOTAL_BYTES; addr += PAGE_SIZE)

#define for_each_page_in_chunk(page_addr, chunk_addr) \
    for (mach_vm_address_t page_addr = chunk_addr; page_addr < chunk_addr + CHUNK_BYTES; page_addr += PAGE_SIZE)

void touch_all_pages_write(mach_vm_address_t base_addr) {
    for_each_page(addr, base_addr) {
        *(volatile uint8_t *)addr = 0xaa;
    }
}

void touch_all_pages_read(mach_vm_address_t base_addr) {
    for_each_page(addr, base_addr) {
        *(volatile uint8_t *)addr;
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

void new_regular_chunk_at(mach_port_t task, mach_vm_address_t addr, mach_vm_size_t chunk_size) {
    CHECK_MACH(mach_vm_allocate(task, &addr, chunk_size, VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE | VM_MAKE_TAG(250)));
}

void remap_at(mach_port_t task, mach_vm_address_t base_addr, mach_vm_size_t size) {
    vm_prot_t cur_protection = VM_PROT_READ | VM_PROT_WRITE;
    vm_prot_t max_protection = VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE;
    CHECK_MACH(mach_vm_remap(task, &base_addr, size, 0, VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE, task, base_addr, 0, &cur_protection, &max_protection, VM_INHERIT_NONE));
}

void hv_touch_memory(hv_vcpu_t vcpu, hv_vcpu_exit_t *exit_reason, mach_vm_address_t addr, size_t size, bool hvc_reuse) {
    CHECK_HV(hv_vcpu_set_reg(vcpu, HV_REG_PC, 0));
    CHECK_HV(hv_vcpu_set_reg(vcpu, HV_REG_X0, hvc_reuse));
    CHECK_HV(hv_vcpu_set_reg(vcpu, HV_REG_X1, addr));
    CHECK_HV(hv_vcpu_set_reg(vcpu, HV_REG_X2, addr + size));

    while (true) {
        CHECK_HV(hv_vcpu_run(vcpu));

        if (exit_reason->reason != HV_EXIT_REASON_EXCEPTION) {
            fprintf(stderr, "unexpected exit reason: %d\n", exit_reason->reason);
            exit(1);
        }

        uint64_t ec = (exit_reason->exception.syndrome >> 26) & 0x3f;
        switch (ec) {
        case 0x1: // WFX
            // done
            return;
        case 0x16: { // HVC
            // this means REUSE
            uint64_t addr;
            CHECK_HV(hv_vcpu_get_reg(vcpu, HV_REG_X1, &addr));
            CHECK_POSIX(madvise((void *)addr, PAGE_SIZE, MADV_FREE_REUSE));
            continue;
        }
        default:
            fprintf(stderr, "unexpected exception syndrome: %llx EC=%llx\n", exit_reason->exception.syndrome, ec);
            exit(1);
        }
    }
}

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

// make the compiler assemble this for us
void guest_payload(void) __attribute__((naked));

void guest_payload(void) {
    asm volatile(
        // x0 = mode. if 1, make a hypercall on each page before touching it
        // x1 = start addr
        // x2 = end addr
        "mov x8, #0xdead\n"

        // loop start
        "1:\n"
        "cmp x1, x2\n"
        "b.ge 3f\n"
        // make a hypercall?
        "cbz x0, 2f\n"
        "hvc #0\n"
        "2:\n"
        // touch the page
        "str x8, [x1]\n"
        // advance to the next page
        "add x1, x1, #16384\n"
        "b 1b\n"

        // end
        "3:\n"
        // wfi to signal done
        "wfi\n"
    );
}

int main(int argc, char **argv) {
    mach_port_t host = mach_host_self();
    mach_port_t task = mach_task_self();

    CHECK_HV(hv_vm_create(NULL));

    hv_vcpu_t vcpu = 0;
    hv_vcpu_exit_t *exit_reason;
    CHECK_HV(hv_vcpu_create(&vcpu, &exit_reason, NULL));
    // DAIF masked, EL1
    CHECK_HV(hv_vcpu_set_reg(vcpu, HV_REG_CPSR, 0x3c0 | 0x5));

    // allocate guest memory
    void *guest_code_mem = mmap(NULL, 16384, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (guest_code_mem == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }

    // copy the guest payload into the guest memory
    memcpy(guest_code_mem, guest_payload, 16384);

    // map the guest memory into the guest's address space
    CHECK_HV(hv_vm_map(guest_code_mem, 0, 16384, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));

    TIME_BLOCK_EACH(mach_task_self, 1000, {
        for (int i = 0; i < 1000; i++) {
            mach_task_self();
        }
    });

    // reserve contig address space
    mach_vm_address_t base_addr = 0;
    // reserve space
    TIME_BLOCK(reserve_space, {
        CHECK_MACH(mach_vm_map(task, &base_addr, TOTAL_BYTES, 0, VM_FLAGS_ANYWHERE | VM_MAKE_TAG(250), 0, 0, 0, VM_PROT_READ | VM_PROT_WRITE, VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE, VM_INHERIT_NONE));
    });

    // map memory in chunks
    TIME_BLOCK_EACH(mach_make_entry_and_map, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            new_regular_chunk_at(task, addr, CHUNK_BYTES);
        }
    });

    // touch all of the memory
    // for (int i = 0; i < 3; i++) {
    //     TIME_BLOCK_EACH(touch_memory, NUM_PAGES, {
    //         touch_all_pages_write(base_addr);
    //     });
    // }

    TIME_BLOCK_EACH(prefault, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES, MADV_WILLNEED));
        }
    });

    // madvise(REUSABLE) for all but 1 page (all_reusable = fastpath)
    TIME_BLOCK_EACH(madvise_reusable, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK_EACH(madvise_reuse, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSE));
        }
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    // remap on host
    TIME_BLOCK(remap_all, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    // the common case is that pages are already in the object, but need to refaulted due to a host-side remap
    TIME_BLOCK_EACH(prefault_and_madvise_reusable, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_WILLNEED));
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK_EACH(madvise_reuse, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSE));
        }
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    // now try the same thing, but with page-by-page faults
    TIME_BLOCK(remap_all, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(retouch_and_madvise_reusable, NUM_CHUNKS, {
        touch_all_pages_read(base_addr);
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK_EACH(madvise_reuse, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSE));
        }
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    // now try the same thing, but with page-by-page faults
    TIME_BLOCK(remap_all, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(zero_prefault_and_madvise_reusable, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_ZERO));
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_WILLNEED));
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK_EACH(madvise_reuse, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES - PAGE_SIZE, MADV_FREE_REUSE));
        }
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    // now try the same thing, but with page-by-page faults
    TIME_BLOCK(remap_all, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(madvise_reusable_page_by_page, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK_EACH(madvise_reuse_all, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, CHUNK_BYTES, MADV_FREE_REUSE));
        }
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    // now try the same thing, but with page-by-page faults
    TIME_BLOCK(remap_all, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(madvise_free_page_by_page_and_remap_amortized, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, PAGE_SIZE, MADV_FREE));
        }
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    TIME_BLOCK(remap_all, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            *(volatile uint8_t *)addr = 0xaa;
        }
    });

    // we run this last because macOS can't coalesce the page-by-page remappings for some reason, causing worse remap/madvise/fault performance for everything after it
    // TIME_BLOCK(remap_all, {
    //     remap_at(task, base_addr, TOTAL_BYTES);
    // });

    // TIME_BLOCK_EACH(madvise_free_page_by_page_and_remap_page_by_page, NUM_CHUNKS, {
    //     for_each_page(addr, base_addr) {
    //         CHECK_POSIX(madvise((void *)addr, PAGE_SIZE, MADV_FREE));
    //         remap_at(task, addr, PAGE_SIZE);
    //     }
    // });

    // TIME_BLOCK_EACH(redirty, NUM_CHUNKS, {
    //     for_each_page(addr, base_addr) {
    //         *(volatile uint8_t *)addr = 0xaa;
    //     }
    // });

    // clear purgable chunks
    // TIME_BLOCK_EACH(purge_purgable, NUM_CHUNKS, {
    //     for_each_chunk(addr, base_addr) {
    //         int state = VM_PURGABLE_EMPTY;
    //         CHECK_MACH(mach_vm_purgable_control(task, addr, VM_PURGABLE_SET_STATE, &state));
    //         state = VM_PURGABLE_NONVOLATILE;
    //         CHECK_MACH(mach_vm_purgable_control(task, addr, VM_PURGABLE_SET_STATE, &state));
    //     }
    // });

    // map all into HV, in one big call
    TIME_BLOCK(hv_map_all, {
        CHECK_HV(hv_vm_map((void*)base_addr, base_addr, TOTAL_BYTES, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));
    });

    // unmap all from HV, in one big call
    TIME_BLOCK(hv_unmap_all, {
        CHECK_HV(hv_vm_unmap(base_addr, TOTAL_BYTES));
    });

    // map into HV, chunk by chunk
    TIME_BLOCK_EACH(hv_map_each, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_HV(hv_vm_map((void*)addr, addr, CHUNK_BYTES, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));
        }
    });

    // unmap from HV, chunk by chunk
    TIME_BLOCK_EACH(hv_unmap_each, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_HV(hv_vm_unmap(addr, CHUNK_BYTES));
        }
    });

    // map all into HV, in one big call
    TIME_BLOCK(hv_map_all, {
        CHECK_HV(hv_vm_map((void*)base_addr, base_addr, TOTAL_BYTES, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));
    });

    TIME_BLOCK(hv_fault_all, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    TIME_BLOCK(remap_all_host, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(hv_remap_and_madvise_reusable_by_page, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_HV(hv_vm_unmap(addr, CHUNK_BYTES));
            for_each_page_in_chunk(page_addr, addr) {
                CHECK_POSIX(madvise((void *)page_addr, PAGE_SIZE, MADV_FREE_REUSABLE));
            }
            CHECK_HV(hv_vm_map((void*)addr, addr, CHUNK_BYTES, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));
        }
    });

    TIME_BLOCK(madvise_reuse, {
        CHECK_POSIX(madvise((void *)base_addr, TOTAL_BYTES, MADV_FREE_REUSE));
    });

    TIME_BLOCK(hv_redirty_all_after_reuse_unmapped, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    TIME_BLOCK(remap_all_host, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(hv_remap_and_madvise_reusable_by_page, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_HV(hv_vm_unmap(addr, CHUNK_BYTES));
            for_each_page_in_chunk(page_addr, addr) {
                CHECK_POSIX(madvise((void *)page_addr, PAGE_SIZE, MADV_FREE_REUSABLE));
            }
            CHECK_HV(hv_vm_map((void*)addr, addr, CHUNK_BYTES, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));
        }
    });

    TIME_BLOCK(hv_redirty_all_after_reusable_unmapped_before_reuse, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    TIME_BLOCK(madvise_reuse_after_hv_redirtied, {
        CHECK_POSIX(madvise((void *)base_addr, TOTAL_BYTES, MADV_FREE_REUSE));
    });

    TIME_BLOCK_EACH(hv_dirtied_madvise_reusable_by_page, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK(remap_all_host, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK(hv_redirty_all_after_reusable_still_mapped, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    TIME_BLOCK_EACH(hv_dirtied_madvise_reusable_by_page, NUM_CHUNKS, {
        for_each_page(addr, base_addr) {
            CHECK_POSIX(madvise((void *)addr, PAGE_SIZE, MADV_FREE_REUSABLE));
        }
    });

    TIME_BLOCK(remap_all_host, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK(hv_redirty_all_hvc_reuse, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, true);
    });

    TIME_BLOCK(remap_all_host, {
        remap_at(task, base_addr, TOTAL_BYTES);
    });

    TIME_BLOCK_EACH(hv_remap_and_madvise_free_by_page, NUM_CHUNKS, {
        for_each_chunk(addr, base_addr) {
            CHECK_HV(hv_vm_unmap(addr, CHUNK_BYTES));
            for_each_page_in_chunk(page_addr, addr) {
                CHECK_POSIX(madvise((void *)page_addr, PAGE_SIZE, MADV_FREE));
            }
            CHECK_HV(hv_vm_map((void*)addr, addr, CHUNK_BYTES, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));
        }
    });

    TIME_BLOCK(hv_redirty_all_after_free_unmapped, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    TIME_BLOCK(hv_redirty_mapped, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    TIME_BLOCK(hv_redirty_mapped, {
        hv_touch_memory(vcpu, exit_reason, base_addr, TOTAL_BYTES, false);
    });

    // touch all of the memory
    for (int i = 0; i < 3; i++) {
        TIME_BLOCK_EACH(touch_memory, NUM_PAGES, {
            touch_all_pages_write(base_addr);
        });
    }

    // // map memory in chunks
    // TIME_BLOCK_EACH(mach_make_entry_and_map, NUM_CHUNKS, {
    //     for_each_chunk(addr, base_addr) {
    //         new_purgable_chunk_at(task, addr, CHUNK_BYTES);
    //     }
    // });

    // unmap memory in chunks
    // TIME_BLOCK_EACH(mach_unmap, NUM_CHUNKS, {
    //     for_each_chunk(addr, base_addr) {
    //         CHECK_MACH(mach_vm_deallocate(task, addr, CHUNK_BYTES));
    //     }
    // });

    volatile char *target_buf = malloc(CHUNK_BYTES * NUM_CHUNKS);
    if (target_buf == NULL) {
        perror("malloc");
        exit(1);
    }
    for (int i = 0; i < 10; i++) {
        TIME_BLOCK_EACH(memcpy_chunk, NUM_CHUNKS, {
            for_each_chunk(addr, base_addr) {
                volatile char *target = target_buf + (addr - base_addr);
                memcpy(target, (void *)addr, CHUNK_BYTES);
                *(volatile uint8_t *)target = 0x00;
            }
        });
    };

    // char *tmp_buf = malloc(CHUNK_BYTES);
    // TIME_BLOCK_EACH(copy_purge_chunk, NUM_CHUNKS, {
    //     for_each_chunk(addr, base_addr) {
    //         char *target = target_buf + (addr - base_addr);
    //         memcpy(tmp_buf, (void *)addr, CHUNK_BYTES);

    //         int state = VM_PURGABLE_EMPTY;
    //         CHECK_MACH(mach_vm_purgable_control(task, addr, VM_PURGABLE_SET_STATE, &state));
    //         state = VM_PURGABLE_NONVOLATILE;
    //         CHECK_MACH(mach_vm_purgable_control(task, addr, VM_PURGABLE_SET_STATE, &state));

    //         memcpy((void *)addr, tmp_buf, CHUNK_BYTES);
    //     }
    // });

    // sleep(100000);

    return 0;
}
