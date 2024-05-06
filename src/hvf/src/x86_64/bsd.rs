//! Register initialization code copied from BSD. This code intentionally tries to replicate BSD's
//! implementation as closely as possible to avoid mistakes since we don't yet really know how x86's
//! virtualization extension works. We should eventually replace this with a from-scratch implementation
//! that properly documents what it's doing.
//!
//!  This series might also be helpful in doing that:
//!   https://rayanfam.com/topics/hypervisor-from-scratch-part-1/

use arch_gen::x86::msr_index::*;

use crate::HvfVcpu;

use super::{
    HV_MSR_IA32_FMASK, HV_MSR_IA32_KERNEL_GS_BASE, HV_MSR_IA32_SYSENTER_CS,
    HV_MSR_IA32_SYSENTER_EIP, HV_MSR_IA32_SYSENTER_ESP, VMCS_CTRL_CPU_BASED, VMCS_CTRL_CPU_BASED2,
    VMCS_CTRL_CR0_MASK, VMCS_CTRL_CR0_SHADOW, VMCS_CTRL_CR4_MASK, VMCS_CTRL_EPTP,
    VMCS_CTRL_EXC_BITMAP, VMCS_CTRL_MSR_BITMAPS, VMCS_CTRL_PIN_BASED, VMCS_CTRL_VMENTRY_CONTROLS,
    VMCS_CTRL_VMEXIT_CONTROLS, VMCS_GUEST_DR7, VMCS_GUEST_LINK_POINTER, VMCS_VPID,
};

const PAGE_SIZE: usize = 4096;

struct VmSetupState {
    msr_bitmap: Box<[u8; PAGE_SIZE]>,
}

impl VmSetupState {
    // sys/amd64/vmm/intel/vmx.c:1045
    fn new() -> Self {
        let mut cx = Self {
            msr_bitmap: Box::new([0xFF; PAGE_SIZE]),
        };

        // Determine VCPU capabilities
        // sys/amd64/vmm/intel/vmx.c:659
        // TODO

        // Determine MSR
        cx.guest_msr_rw(MSR_GS_BASE);
        cx.guest_msr_rw(MSR_FS_BASE);
        cx.guest_msr_rw(HV_MSR_IA32_SYSENTER_CS); // MSR_SYSENTER_CS_MSR
        cx.guest_msr_rw(HV_MSR_IA32_SYSENTER_ESP); // MSR_SYSENTER_ESP_MSR
        cx.guest_msr_rw(HV_MSR_IA32_SYSENTER_EIP); // MSR_SYSENTER_EIP_MSR
        cx.guest_msr_rw(MSR_EFER);
        cx.guest_msr_ro(MSR_IA32_TSC); // MSR_TSC
        cx.guest_msr_ro(MSR_TSC_AUX);

        cx
    }

    // Unsafe because `VmSetupState` must be alive for... TBD? I mean, it must at least as long as
    // the HVF is being set-up. Usually, `msr_bitmap` is a physical address but the kernel (presumably)
    // does the translation for us.
    //
    // sys/amd64/vmm/intel/vmx.c:1121
    unsafe fn vmx_vcpu_init(&mut self, vcpu: &mut HvfVcpu) {
        self.vmx_msr_guest_init();
        self.vmcs_init(vcpu);

        vcpu.write_vmcs(VMCS_CTRL_EPTP, !0).unwrap(); // VMCS_EPTP

        // TODO: Set (See: 25.6.1 Pin-Based VM-Execution Controls)
        vcpu.write_vmcs(VMCS_CTRL_PIN_BASED, 0).unwrap(); // VMCS_PIN_BASED_CTLS

        // TODO: Set (See: 25.6.2 Processor-Based VM-Execution Controls)
        vcpu.write_vmcs(VMCS_CTRL_CPU_BASED, 0).unwrap(); // VMCS_PRI_PROC_BASED_CTLS

        // TODO: Set (See: 25.6.2 Processor-Based VM-Execution Controls)
        vcpu.write_vmcs(VMCS_CTRL_CPU_BASED2, 0).unwrap(); // VMCS_SEC_PROC_BASED_CTLS

        // TODO: Set (See: 25.7.1 VM-Exit Controls)
        vcpu.write_vmcs(VMCS_CTRL_VMEXIT_CONTROLS, 0).unwrap(); // VMCS_EXIT_CTLS

        // TODO: Set (See: 25.8.1 VM-Entry Controls)
        vcpu.write_vmcs(VMCS_CTRL_VMENTRY_CONTROLS, 0).unwrap(); // VMCS_ENTRY_CTLS

        let msr_bitmap = self.msr_bitmap.as_ptr() as u64;
        vcpu.write_vmcs(VMCS_CTRL_MSR_BITMAPS, msr_bitmap).unwrap(); // VMCS_MSR_BITMAP

        // TODO: This might require some capability checking (see: 29.1 VIRTUAL PROCESSOR IDENTIFIERS)
        // afaict, these can just be arbitrary u16s
        vcpu.write_vmcs(VMCS_VPID, vcpu.vcpuid as u64).unwrap();

        // TODO: These might also need to be set...
        // if (guest_l1d_flush && !guest_l1d_flush_sw) {
        //     vmcs_write(VMCS_ENTRY_MSR_LOAD, pmap_kextract(
        //         (vm_offset_t)&msr_load_list[0]));
        //     vmcs_write(VMCS_ENTRY_MSR_LOAD_COUNT,
        //         nitems(msr_load_list));
        //     vmcs_write(VMCS_EXIT_MSR_STORE, 0);
        //     vmcs_write(VMCS_EXIT_MSR_STORE_COUNT, 0);
        // }

        // This assumes that `vcpu_trace_exceptions` is false.
        vcpu.write_vmcs(VMCS_CTRL_EXC_BITMAP, 1 << 18).unwrap(); // VMCS_EXCEPTION_BITMAP

        vcpu.write_vmcs(VMCS_GUEST_DR7, 1024).unwrap(); // DBREG_DR7_RESERVED1

        // TODO: Do we need any of these features?
        //     if (tpr_shadowing) {
        //         error += vmwrite(VMCS_VIRTUAL_APIC, vtophys(vcpu->apic_page));
        //     }
        //
        //     if (virtual_interrupt_delivery) {
        //         error += vmwrite(VMCS_APIC_ACCESS, APIC_ACCESS_ADDRESS);
        //         error += vmwrite(VMCS_EOI_EXIT0, 0);
        //         error += vmwrite(VMCS_EOI_EXIT1, 0);
        //         error += vmwrite(VMCS_EOI_EXIT2, 0);
        //         error += vmwrite(VMCS_EOI_EXIT3, 0);
        //     }
        //     if (posted_interrupts) {
        //         error += vmwrite(VMCS_PIR_VECTOR, pirvec);
        //         error += vmwrite(VMCS_PIR_DESC, vtophys(vcpu->pir_desc));
        //     }

        self.vmx_setup_cr0_shadow(vcpu, 0x60000010);
        self.vmx_setup_cr4_shadow(vcpu, 0);
    }

    // sys/amd64/vmm/intel/vmcs.c:341
    fn vmcs_init(&mut self, vcpu: &mut HvfVcpu) {
        // (everything else in this function is for the host)

        vcpu.write_vmcs(VMCS_GUEST_LINK_POINTER, !0).unwrap(); // VMCS_LINK_POINTER
    }

    // sys/amd64/vmm/intel/vmx_msr.c:312
    fn vmx_msr_guest_init(&mut self) {
        self.guest_msr_rw(MSR_LSTAR);
        self.guest_msr_rw(MSR_CSTAR);
        self.guest_msr_rw(MSR_STAR);
        self.guest_msr_rw(HV_MSR_IA32_FMASK); // MSR_SF_MASK
        self.guest_msr_rw(HV_MSR_IA32_KERNEL_GS_BASE); // MSR_KGSBASE
    }

    // sys/amd64/vmm/intel/vmx_msr.h:66
    fn guest_msr_rw(&mut self, msr: u32) {
        self.msr_bitmap_change_access(msr, BitmapAccess::RW);
    }

    // sys/amd64/vmm/intel/vmx_msr.h:69
    fn guest_msr_ro(&mut self, msr: u32) {
        self.msr_bitmap_change_access(msr, BitmapAccess::READ);
    }

    // sys/amd64/vmm/intel/vmx_msr.c:143
    // This seems to follow the algorithm described at 25.6.9 MSR-Bitmap Address
    fn msr_bitmap_change_access(&mut self, msr: u32, access: BitmapAccess) {
        let byte = if msr <= 0x00001FFF {
            msr / 8
        } else if (0xC0000000..=0xC0001FFF).contains(&msr) {
            1024 + (msr - 0xC0000000) / 8
        } else {
            panic!("invalid MSR");
        };

        let bit = msr & 0x7;

        if access.intersects(BitmapAccess::READ) {
            self.msr_bitmap[byte as usize] &= !(1 << bit);
        } else {
            self.msr_bitmap[byte as usize] |= 1 << bit;
        }

        let byte = byte + 2048;

        if access.intersects(BitmapAccess::WRITE) {
            self.msr_bitmap[byte as usize] &= !(1 << bit);
        } else {
            self.msr_bitmap[byte as usize] |= 1 << bit;
        }
    }

    // sys/amd64/vmm/intel/vmx_msr.c:1041
    fn vmx_setup_cr0_shadow(&mut self, vcpu: &mut HvfVcpu, initial: u32) {
        self.vmx_setup_cr_shadow(vcpu, 0, initial);
    }

    // sys/amd64/vmm/intel/vmx_msr.c:1042
    fn vmx_setup_cr4_shadow(&mut self, vcpu: &mut HvfVcpu, initial: u32) {
        self.vmx_setup_cr_shadow(vcpu, 4, initial);
    }

    // sys/amd64/vmm/intel/vmx_msr.c:1013
    fn vmx_setup_cr_shadow(&mut self, vcpu: &mut HvfVcpu, which: u32, initial: u32) {
        if which != 0 && which != 4 {
            panic!("vmx_setup_cr_shadow: unknown cr{which}");
        }

        let mask_ident: u32;
        let mask_value: u64;
        let shadow_ident: u32;

        if which == 0 {
            mask_ident = VMCS_CTRL_CR0_MASK; // VMCS_CR0_MASK

            // TODO: What are these mask values? (was `cr0_ones_mask | cr0_zeros_mask` but controlled by a sysctl)
            mask_value = 0;

            shadow_ident = VMCS_CTRL_CR0_SHADOW; // VMCS_CR0_SHADOW
        } else {
            mask_ident = VMCS_CTRL_CR4_MASK; // VMCS_CR4_MASK

            // TODO: ibid (`cr4_ones_mask | cr4_zeros_mask`)
            mask_value = 0;

            shadow_ident = VMCS_CTRL_CR0_SHADOW; // VMCS_CR4_SHADOW
        }

        // TODO: These also need to be fixed by some mask...?
        vcpu.write_reg(mask_ident, mask_value).unwrap();
        vcpu.write_reg(shadow_ident, initial as u64).unwrap();
    }
}

bitflags::bitflags! {
    pub struct BitmapAccess: u32 {
        const READ = 0x1;
        const WRITE = 0x2;
        const RW = Self::READ.bits() | Self::WRITE.bits();
    }
}
