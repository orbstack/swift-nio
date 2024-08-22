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
        "mov x0, #0xdead\n"
        "mov x1, 0\n"
        "mov x2, 0\n"
        "1:\n"
        "mrs x1, cntvct_el0\n"
        "str x1, [x5]\n"
        "mrs x2, cntvct_el0\n"
        "sub x0, x2, x1\n"
        "b 1b\n"
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
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, 0x10000000));
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X5, 0x80));

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
        if (ec != 0x24) {
            printf("Unexpected exception syndrome: %llx\n", exit_reason->exception.syndrome);
            exit(1);
        }

        // advance PC
        uint64_t pc;
        check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_PC, &pc));
        pc += 4;
        check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, pc));

        // ignore initial 0xdead
        if (i == 0) {
            continue;
        }

        // read the time delta calculated by guest
        uint64_t delta;
        check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_X0, &delta));

        // read sysregs
        // uint64_t cntv_cval_el0;
        // check_hvf(hv_vcpu_get_sys_reg(vcpu, HV_SYS_REG_CNTV_CVAL_EL0, &cntv_cval_el0));

        acc_delta += delta;
    }

    uint64_t avg_delta = acc_delta / ITERS;
    // convert to nanoseconds
    avg_delta *= timebase.numer;
    avg_delta /= timebase.denom;

    // result on M3 Max, macOS 14.6.1: 583 ns, 708 ns with sysreg read, 666 ns with pending IRQ
    printf("avg MMIO time: %lld ns\n", avg_delta);

    return 0;
}
