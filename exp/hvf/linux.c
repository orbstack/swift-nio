#include <Hypervisor/Hypervisor.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mman.h>
#include <mach/mach_time.h>
#include <mach/mach_vm.h>
#include <mach/mach_init.h>
#include <string.h>
#include <pthread.h>
#include <fcntl.h>
#include <unistd.h>

void check_hvf(hv_return_t ret) {
    if (ret != HV_SUCCESS) {
        printf("HVF error: %d\n", ret);
        exit(1);
    }
}

/*
 * Simple VMM with no devices.
 * Boots Linux kernel up to the point where it tries to mount rootfs.
 *
 * Caveats:
 *   - timer fails to init because no interrupt controller (so standard kernel Image doesn't boot because raid6 benchmark gets stuck waititng for jiffies time to advance)
 *   - PL011 serial implementation is minimal and only works with the earlycon driver, so keep_bootcon is needed
 */
int main(int argc, const char * argv[]) {
    if (argc != 3) {
        printf("Usage: %s <kernel Image> <fdt>\n", argv[0]);
        exit(1);
    }

    // create VM with default settings
    check_hvf(hv_vm_create(NULL));

    // allocate guest memory
    void *guest_mem = mmap(NULL, 128ULL * 1024 * 1024, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (guest_mem == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }

    // open kernel file
    int fd = open(argv[1], O_RDONLY | O_CLOEXEC);
    if (fd < 0) {
        perror("open");
        exit(1);
    }

    // read kernel to start of allocated guest memory
    size_t expected_len = lseek(fd, 0, SEEK_END);
    lseek(fd, 0, SEEK_SET);
    size_t len = read(fd, guest_mem, 128ULL * 1024 * 1024);
    if (len < 0) {
        perror("read");
        exit(1);
    }
    if (len != expected_len) {
        printf("Unexpected length: %ld\n", len);
        exit(1);
    }

    // map the guest memory into the guest's address space
    check_hvf(hv_vm_map(guest_mem, 0x10000000, 128ULL * 1024 * 1024, HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC));

    // open FDT (compiled device tree)
    // FDT contains system config info: RAM location, size, kernel cmdline, CPUs, timers, interrupts, devices, etc.
    close(fd);
    fd = open(argv[2], O_RDONLY | O_CLOEXEC);
    if (fd < 0) {
        perror("open");
        exit(1);
    }

    // allocate memory for FDT
    void *guest_mem2 = mmap(NULL, 128ULL * 1024 * 1024, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (guest_mem2 == MAP_FAILED) {
        perror("mmap");
        exit(1);
    }

    // read FDT
    len = read(fd, guest_mem2, 128ULL * 1024 * 1024);
    if (len < 0) {
        perror("read");
        exit(1);
    }

    // map FDT into guest's address space
    check_hvf(hv_vm_map(guest_mem2, 0x20000000, 128ULL * 1024 * 1024, HV_MEMORY_READ));

    close(fd);

    // create 1 vcpu
    hv_vcpu_t vcpu;
    hv_vcpu_exit_t *exit_reason;
    check_hvf(hv_vcpu_create(&vcpu, &exit_reason, NULL));

    // Current Program Status Register (CPSR) = 0x3c0 | 0x5
    // 0x3c0 = DAIF (IRQ, FIQ, debug, and async exceptions masked, because exception vectors aren't ready at boot)
    // 0x5 = mode EL1h (EL1 with SP register = SP_EL1 sysreg)
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_CPSR, 0x3c0 | 0x5));

    // Program Counter (PC) = 0x10000000 (entry point = start of image on arm64)
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, 0x10000000));
    // Linux arm64 boot protocol:
    // x0 = address of FDT
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X0, 0x20000000));
    // x1..x3 = 0
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X1, 0));
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X2, 0));
    check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X3, 0));

    while (true) {
        // run the guest until an exit occurs
        check_hvf(hv_vcpu_run(vcpu));

        // we never force-cancel the vCPU, and don't set up timers, so the only exit reason should be an exception
        if (exit_reason->reason != HV_EXIT_REASON_EXCEPTION) {
            printf("Unexpected exit reason: %d\n", exit_reason->reason);
            exit(1);
        }

        uint64_t ec = (exit_reason->exception.syndrome >> 26) & 0x3f;

        // HVF exit reason (always EXCEPTION)
        printf("exit reason: %d\n", exit_reason->reason);
        // syndrome = ESR_EL2 = exception info
        printf("ESR_EL2 = %llx\n", exit_reason->exception.syndrome);
        // EC = exception class (exception type/reason)
        printf("  EC = %llx\n", (exit_reason->exception.syndrome >> 26) & 0x3f);
        // FAR_EL2 = faulting EL1/EL0 virtual address
        printf("FAR_EL2 = %llx\n", exit_reason->exception.virtual_address);
        // HPFAR_EL2 = faulting EL2 physical address (IPA)
        // IPA = Intermediate Physical Address (i.e. VM physical address)
        printf("HPFAR_EL2 = %llx\n", exit_reason->exception.physical_address);

        // each EC is a trap reason/type
        switch (ec) {
            // data abort: memory read/write fault
            case 0x24: {
                // srt = register index for read/write
                uint64_t srt = (exit_reason->exception.syndrome >> 16) & 0x1f;
                switch (exit_reason->exception.physical_address) {
                    // PL011 UART serial device is located at 0x80000000
                    // it's not mapped as memory, so data aborts go to us

                    // PL011 DR (data register)
                    case 0x80000000: {
                        // we should check whether this is a read or write, but for now, assume write

                        // read written value from operand register
                        uint64_t val;
                        check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_X0 + srt, &val));

                        // write 1 byte of serial console output to stderr
                        uint8_t ch = val & 0xff;
                        write(2, &ch, 1);

                        break;
                    }

                    // PL011 FR (flag register)
                    case 0x80000018:
                        // don't set BUSY or TXFF
                        check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X0 + srt, 0));
                        break;

                    default:
                        printf("Unexpected HPFAR_EL2: %llx\n", exit_reason->exception.physical_address);
                        exit(1);
                }

                // advance PC (skip this instruction; we've emulated it)
                uint64_t val;
                check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_PC, &val));
                check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, val + 4));

                break;
            }

            // WFI (Wait For Interrupt) instruction
            // this is used for idling the virtual CPU when it has nothing to do
            case 0x1: {
                // implement this as a no-op (so we busy loop in the guest)

                // advance PC (skip this instruction; we've emulated it)
                uint64_t val;
                check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_PC, &val));
                check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, val + 4));

                break;
            }

            // HVC (Hypervisor Call) instruction
            case 0x16: {
                // SMCCC ABI: return value in x0
                // -1 = not supported
                check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X0, -1));

                // advancing PC is not needed: CPU does it automatically for HVC
                break;
            }

            // SMC (Secure Monitor Call) instruction
            case 0x17: {
                // SMCCC ABI: return value in x0
                // -1 = not supported
                check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X0, -1));

                // advancing PC is not needed: CPU does it automatically for SMC
                break;
            }

            // MSR/MRS (Move to/from System Register) instruction
            // Linux triggers this on boot even if there's no GICv3 interrupt controller, because it tries to detect CPU feature in registers that aren't supported by HVF/Apple CPUs
            case 0x18: {
                bool is_read = (exit_reason->exception.syndrome & 1) != 0;
                uint64_t arg_reg_idx = (exit_reason->exception.syndrome >> 5) & 0x1f;

                if (is_read) {
                    // MRS uses the encoding where Rt=31 means xzr, not x31
                    uint64_t val;
                    if (arg_reg_idx != 31)
                        check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_X0 + arg_reg_idx, 0));
                } else {
                    // MSR is a no-op
                }

                // advance PC (skip this instruction; we've emulated it)
                uint64_t val;
                check_hvf(hv_vcpu_get_reg(vcpu, HV_REG_PC, &val));
                check_hvf(hv_vcpu_set_reg(vcpu, HV_REG_PC, val + 4));

                break;
            }

            default: {
                printf("Unexpected exception syndrome: %llx\n", exit_reason->exception.syndrome);
                exit(1);
            }
        }
    }

    return 0;
}
