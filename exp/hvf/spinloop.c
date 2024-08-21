#include <Hypervisor/Hypervisor.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <mach/mach_time.h>
#include <string.h>

// make the compiler assemble this for us
void guest_payload(void) __attribute__((naked));

void guest_payload(void) {
    asm volatile(
        "add x1, x1, #256\n"
        "1:\n"

        // 2500 mW
        // "isb sy\n"

        // 5000 mW
        // "yield\n"

        // 5000 mW
        // "nop\n"

        // 6000 mW
        // "mrs x3, cntvct_el0\n"

        // 2500 mW
        // "mrs x3, cntvctss_el0\n"

        // 2200 mW
        // "sevl\n"
        // "wfe\n"

        // 1600 mW
        // "wfe\n"

        // 2600 mW
        // "isb sy\n"
        // "mrs x3, cntvct_el0\n"

        // 2500 mW
        // "isb sy\n"
        // "mrs x3, cntvct_el0\n"
        // // linux arch_counter_enforce_ordering
        // "eor x4, x3, x3\n"
        // "add x4, sp, x4\n"
        // "ldr xzr, [x4]\n"

        // 2200 mW
        "sevl\n"
        "wfe\n"
        "mrs x3, cntvct_el0\n"

        "ldr x0, [x1]\n"
        "cbnz x0, 2f\n"
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

    // run the guest
    check_hvf(hv_vcpu_run(vcpu));

    // check the exception type
    printf("Exit reason: %d\n", exit_reason->reason);
    printf("Unexpected exception syndrome: %llx\n", exit_reason->exception.syndrome);
    exit(1);

    return 0;
}
