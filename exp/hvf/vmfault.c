#include <Hypervisor/Hypervisor.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <mach/mach_time.h>
#include <mach/mach_vm.h>
#include <mach/mach_init.h>
#include <string.h>
#include <pthread.h>

// make the compiler assemble this for us
void guest_payload(void) __attribute__((naked));

void guest_payload(void) {
    asm volatile(
        "mov x0, #0xdead\n"
        "mov x1, 0\n"
        "mov x2, 0\n"
        "1:\n"
        "mrs x1, cntvct_el0\n"
        "ldr x8, [x5]\n"
        "mrs x2, cntvct_el0\n"
        "sub x0, x2, x1\n"
        "hvc #0\n"
        "b 1b\n"
    );
}

void check_hvf(hv_return_t ret) {
    if (ret != HV_SUCCESS) {
        printf("HVF error: %d\n", ret);
        exit(1);
    }
}

#define ITERS 2000000
#define WORKERS 1

void *worker(void* context) {
    uint64_t anon_guest_addr = 0x80000000 + 0x100000 * (int)context;

    hv_vcpu_t vcpu;
    hv_vcpu_exit_t *exit_reason;
    check_hvf(hv_vcpu_create(&vcpu, &exit_reason, NULL));

    // allocate anon memory
    void *anon_mem = mmap(NULL, 16384, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (anon_mem == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }

    // write something
    uint64_t val = 0x12345678;
    memcpy(anon_mem, &val, sizeof(val));

    // map the anon memory into the guest's address space
    check_hvf(hv_vm_map(anon_mem, anon_guest_addr, 16384, HV_MEMORY_READ | HV_MEMORY_WRITE));

    // set the guest's instruction pointer to the start of the guest memory
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, 0x10000000));
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X5, anon_guest_addr));

    // boot in EL1, mask DAIF
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_CPSR, 0x3c0 | 0x5));

    mach_timebase_info_data_t timebase;
    mach_timebase_info(&timebase);

    uint64_t acc_delta = 0;
    for (int i = 0; i < ITERS; i++) {
        // simulate pending interrupts
        // when benchmarking linux with IRQs masked, this is expected due to pending sched tick vtimer
        // check_hvf(hv_vcpu_set_pending_interrupt(vcpu, HV_INTERRUPT_TYPE_IRQ, true));

        // run the guest
        check_hvf(hv_vcpu_run(vcpu));

        // check the exit reason
        if (exit_reason->reason != HV_EXIT_REASON_EXCEPTION) {
            printf("Unexpected exit reason: %d\n", exit_reason->reason);
            exit(1);
        }

        // check the exception type
        uint64_t ec = (exit_reason->exception.syndrome >> 26) & 0x3f;
        if (ec != 0x16) {
            printf("Unexpected exception syndrome: %llx\n", exit_reason->exception.syndrome);
            exit(1);
        }

        // read the time delta calculated by guest
        uint64_t delta;
        check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_X0, &delta));

        // read sysregs
        // uint64_t cntv_cval_el0;
        // check_hvf(hv_vcpu_get_sys_reg(vcpu, HV_SYS_REG_CNTV_CVAL_EL0, &cntv_cval_el0));

        // we never cleared REUSABLE. do it now
        // madvise(anon_mem, 16384, MADV_FREE_REUSE);

        // remap memory
        check_hvf(hv_vm_unmap(anon_guest_addr, 16384));

        // trigger a fast fault
        // madvise(anon_mem, 16384, MADV_FREE_REUSE);
        // memcpy(anon_mem, &val, sizeof(val));
        // // retouch it on host
        // uint64_t before = mach_absolute_time();
        // // memcpy(anon_mem, &val, sizeof(val));
        // mach_vm_address_t addr = (mach_vm_address_t)anon_mem;
        // int cur_prot = VM_PROT_READ | VM_PROT_WRITE;
        // int max_prot = VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE;
        // kern_return_t ret = mach_vm_remap(mach_task_self(), &addr, 16384, 0, VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE, mach_task_self(), addr, 0, &cur_prot, &max_prot, VM_INHERIT_NONE);
        // if (ret != KERN_SUCCESS) {
        //     exit(1);
        // }
        // uint64_t after = mach_absolute_time();
        // madvise(anon_mem, 16384, MADV_FREE_REUSABLE);

        // make a new chunk to test new page insertion speed
        // void *anon_mem = mmap(NULL, 16384, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
        // if (anon_mem == MAP_FAILED) {
        //     perror("mmap");
        //     exit(1);
        // }

        check_hvf(hv_vm_map(anon_mem, anon_guest_addr, 16384, HV_MEMORY_READ | HV_MEMORY_WRITE));

        acc_delta += delta;//(after - before);
    }

    uint64_t avg_delta = acc_delta / ITERS;
    // convert to nanoseconds
    avg_delta *= timebase.numer;
    avg_delta /= timebase.denom;

    // result on M3 Max, macOS 14.6.1:
    // - 916 ns with hv unmap+map
    //   - vm_fault_attempt_pmap_enter -> pmap_tt_allocate
    // - 1208 ns with MADV_DONTNEED
    //   - handle_guest_abort -> arm_fast_fault
    //   probably because it also clears from host PTEs (so two PTEs to update)
    // - 0 ns with MADV_DONTNEED + host retouch(833ns host)
    // - 1208 ns with MADV_FREE
    //   - vm_map_msync -> vm_object_deactivate_pages -> deactivate_a_chunk -> phys_attribute_clear_with_flush_range -> arm_force_fast_fault_with_flush_range
    //   - handle_guest_abort -> arm_fast_fault
    // - 0 ns with MADV_FREE + host retouch(833ns host)
    // - 1166 ns with MADV_FREE_REUSABLE
    // - 0 ns with MADV_FREE_REUSABLE + host retouch(833ns host)
    // - 0 ns with MADV_FREE_REUSABLE + retouch(833ns host) + MADV_FREE_REUSE(208ns host)
    // - 0 ns with MADV_FREE_REUSABLE + MADV_FREE_REUSE(833ns host)
    //   - works because reuse calls *arm_clear_fast_fault*!
    // - 916 ns with unmap + MADV_FREE_REUSABLE + map
    // pure host fast fault cost = 458 ns to clear a REUSABLE fast fault, when not mapped in VM. 833ns to clear fast fault on host when mapped in both VM and host pmaps. mach_vm_remap = 1041 ns
    // TODO: spindump to see why
    printf("avg VM_fault time: %lld ns\n", avg_delta);

    return NULL;
}

int main(int argc, const char * argv[]) {
    check_hvf(hv_vm_create(NULL));

    // allocate guest memory
    void *guest_mem = mmap(NULL, 16384, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (guest_mem == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }

    // copy the guest payload into the guest memory
    memcpy(guest_mem, guest_payload, 16384);

    // map the guest memory into the guest's address space
    check_hvf(hv_vm_map(guest_mem, 0x10000000, 16384, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));

    pthread_t *threads = malloc(WORKERS * sizeof(pthread_t));
    for (int i = 0; i < WORKERS; i++) {
        pthread_create(&threads[i], NULL, worker, (void*)i);
    }
    for (int i = 0; i < WORKERS; i++) {
        pthread_join(threads[i], NULL);
    }

    return 0;
}
