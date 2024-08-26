#include <Hypervisor/Hypervisor.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <mach/mach_time.h>
#include <string.h>

uint64_t nsec_to_mabs(uint64_t nsec) {
    mach_timebase_info_data_t timebase;
    mach_timebase_info(&timebase);
    return nsec * timebase.denom / timebase.numer;
}

// make the compiler assemble this for us
void guest_payload(void) __attribute__((naked));

/*
raw overhead of instructions, without memory load, all with cntvct(ss)_el0 at the end:

yield:
Rate: 3832.06M ops/sec
Time per op: 0.26 ns

nop:
Rate: 3815.44M ops/sec
Time per op: 0.26 ns

only mrs cntvct_el0:
Rate: 3804.87M ops/sec
Time per op: 0.26 ns

mrs cntvctss_el0:
Rate: 244.77M ops/sec
Time per op: 4.09 ns

isb + mrs cntvct_el0:
Rate: 122.58M ops/sec
Time per op: 8.16 ns

sevl + wfe:
Rate: 103.65M ops/sec
Time per op: 9.65 ns

wfe:
Rate: 0.75M ops/sec
Time per op: 1340.00 ns



WITH memory load + cbnz:

yield + cntvct:
Rate: 2188.75M ops/sec
Time per op: 0.46 ns

nop + cntvct:
Rate: 2196.23M ops/sec
Time per op: 0.46 ns

only cntvct:
Rate: 2165.30M ops/sec
Time per op: 0.46 ns

cntvctss:
Rate: 244.07M ops/sec
Time per op: 4.10 ns

isb + cntvct:
Rate: 123.35M ops/sec
Time per op: 8.11 ns

sevl + wfe + cntvct:
Rate: 103.18M ops/sec
Time per op: 9.69 ns

isb + cntvct + isb:
Rate: 61.21M ops/sec
Time per op: 16.34 ns

cntvctss + isb:
Rate: 81.61M ops/sec
Time per op: 12.25 ns

wfe + cntvct:
Rate: 0.75M ops/sec
Time per op: 1337.80 ns

isb + cntvct + eor dependent memory load:
Rate: 98.39M ops/sec
Time per op: 10.16 ns

cntvct + eor dependent memory load:
Rate: 2092.94M ops/sec
Time per op: 0.48 ns
 */
void guest_payload(void) {
    asm volatile(
        "mov x8, #0\n"
        "add x1, x1, #256\n"
        "1:\n"

        // 2500 mW
        // "isb sy\n"

        // 5000 mW
        // "yield\n"

        // 5000 mW
        // "nop\n"

        // 6000 mW; 2184M loads/sec
        // "mrs x3, cntvct_el0\n"

        // doesn't work on M1, M1 Max, or M3
        // "mrs x3, S3_4_c15_c10_6\n" // ACNTVCT_EL0 (pre-standard CNTVCTSS_EL0)

        // 2500 mW; 245M loads/sec
        // (aug 26) 1900 mW
        // "mrs x3, cntvctss_el0\n"

        // 2200 mW
        // "sevl\n"
        // "wfe\n"

        // 1600 mW
        // "wfe\n"

        // 2600 mW; 123M loads/sec
        // (aug 26) 1850 mW
        // "isb sy\n"
        // "mrs x3, cntvct_el0\n"

        // 2500 mW
        // "isb sy\n"
        // "mrs x3, cntvct_el0\n"
        // "isb sy\n"

        // (aug 26) 1750 mW
        // "mrs x3, cntvctss_el0\n"
        // "isb sy\n"

        // 2500 mW
        // "isb sy\n"
        // "mrs x3, cntvct_el0\n"
        // // linux arch_counter_enforce_ordering
        // "eor x4, x3, x3\n"
        // "add x4, sp, x4\n"
        // "ldr xzr, [x4]\n"

        // 6000 mW; 2095M ops/sec
        // "mrs x3, cntvct_el0\n"
        // // linux arch_counter_enforce_ordering
        // "eor x4, x3, x3\n"
        // "add x4, sp, x4\n"
        // "ldr xzr, [x4]\n"

        // 2200 mW
        // "sevl\n"
        // "wfe\n"
        // "mrs x3, cntvct_el0\n"

        "ldr x0, [x1]\n"
        "cbnz x0, 2f\n"
        "add x8, x8, #1\n"

        // comment these two instructions to make it run forever for powermetrics --samplers cpu_power
        "cmp x3, x10\n"
        "b.ge 2f\n"

        "b 1b\n"
        "2:\n"
        "hvc #0\n"
    );
}

void check_hvf(hv_return_t ret) {
    if (ret != HV_SUCCESS) {
        printf("HVF error: %d\n", ret);
        exit(1);
    }
}

#define ITERS 10000000

int main(int argc, const char * argv[]) {
    check_hvf(hv_vm_create(NULL));

    hv_vcpu_t vcpu;
    hv_vcpu_exit_t *exit_reason;
    check_hvf(hv_vcpu_create(&vcpu, &exit_reason, NULL));

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

    // set the guest's instruction pointer to the start of the guest memory
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X1, 0x10000000));
    check_hvf(hv_vcpu_set_sys_reg(vcpu, HV_SYS_REG_SP_EL1, 0x10000000));
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, 0x10000000));

    // set the value we read to 0
    ((uint64_t*)guest_mem)[256/8] = 0;

    // boot in EL1, mask DAIF
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_CPSR, 0x3c0 | 0x5));

    // simulate pending interrupts
    // when benchmarking linux with IRQs masked, this is expected due to pending sched tick vtimer
    // check_hvf(hv_vcpu_set_pending_interrupt(vcpu, HV_INTERRUPT_TYPE_IRQ, true));

    // set deadline
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X10, mach_absolute_time() + nsec_to_mabs(5ULL * NSEC_PER_SEC)));

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

    // read count
    uint64_t num_loads;
    check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_X8, &num_loads));

    // get rate
    printf("Rate: %.2fM ops/sec\n", (double)num_loads / 5 / 1e6);
    printf("Time per op: %.2f ns\n", 5.0 / num_loads * 1e9);

    return 0;
}
