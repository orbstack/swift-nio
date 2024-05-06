//! Register initialization code copied from xhyve. This code intentionally tries to replicate xhyve's
//! implementation as closely as possible to avoid mistakes since we don't yet really know how x86's
//! virtualization extension works. We should eventually replace this with a from-scratch implementation
//! that properly documents what it's doing. [This series](hypervisor-tutorial) might also be helpful
//! in doing that.
//!
//! [hypervisor-tutorial]: https://rayanfam.com/topics/hypervisor-from-scratch-part-1/
//!
//! ## References
//!
//! - BSD: `HEAD` is `fce03f85c5bfc0d73fb5c43ac1affad73efab11a` (May 5, 2024)
//! - xhyve: `HEAD` is `dfbe09b9db0ef9384c993db8e72fb3e96f376e7b` (Oct 2, 2021)
//!
//! ## Copyright Notices
//!
//! ```plaintext
//! Copyright (c) 2011 NetApp, Inc.
//! Copyright (c) 2015 xhyve developers
//!
//! All rights reserved.
//!
//! Redistribution and use in source and binary forms, with or without
//! modification, are permitted provided that the following conditions
//! are met:
//! 1. Redistributions of source code must retain the above copyright
//!    notice, this list of conditions and the following disclaimer.
//! 2. Redistributions in binary form must reproduce the above copyright
//!    notice, this list of conditions and the following disclaimer in the
//!    documentation and/or other materials provided with the distribution.
//!
//! THIS SOFTWARE IS PROVIDED BY NETAPP, INC ``AS IS'' AND
//! ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
//! IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
//! ARE DISCLAIMED.  IN NO EVENT SHALL NETAPP, INC OR CONTRIBUTORS BE LIABLE
//! FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
//! DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
//! OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
//! HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
//! LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY
//! OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF
//! SUCH DAMAGE.
//!
//! $FreeBSD$
//! ```

use anyhow::Context;

use crate::HvfVcpu;

use super::{
    hv_vmx_capability_t_HV_VMX_CAP_ENTRY, hv_vmx_capability_t_HV_VMX_CAP_EXIT,
    hv_vmx_capability_t_HV_VMX_CAP_PINBASED, hv_vmx_capability_t_HV_VMX_CAP_PROCBASED,
    hv_vmx_capability_t_HV_VMX_CAP_PROCBASED2,
};

mod constants;
use constants::*;

struct VmSetupState {
    state: Vec<()>,
}

impl VmSetupState {
    fn new(cpu_count: usize) -> Self {
        todo!();
    }

    // xhyve: src/vmm/intel/vmx.c:567
    fn vmx_vcpu_init(&mut self, vcpu: &HvfVcpu) -> anyhow::Result<()> {
        // xhyve: src/vmm/intel/vmx.c:584
        vcpu.enable_native_msr(MSR_GSBASE)?;
        vcpu.enable_native_msr(MSR_FSBASE)?;
        vcpu.enable_native_msr(MSR_SYSENTER_CS_MSR)?;
        vcpu.enable_native_msr(MSR_SYSENTER_ESP_MSR)?;
        vcpu.enable_native_msr(MSR_SYSENTER_EIP_MSR)?;
        vcpu.enable_native_msr(MSR_TSC)?;
        vcpu.enable_native_msr(MSR_IA32_TSC_AUX)?;

        // xhyve: src/vmm/intel/vmx.c:595
        self.vmx_msr_guest_init(vcpu)?;

        // xhyve: src/vmm/intel/vmx.c:597
        let mut pinbased_ctls = 0u32;
        let mut procbased_ctls = 0u32;
        let mut procbased_ctls2 = 0u32;
        let mut exit_ctls = 0u32;
        self.state[vcpu.id() as usize].entry_ctls = 0;

        // Check support for primary processor-based VM-execution controls
        // xhyve: src/vmm/intel/vmx.c:603
        Self::vmx_set_ctlreg(
            vcpu,
            0x00004002,                               // VMCS_PRI_PROC_BASED_CTLS
            hv_vmx_capability_t_HV_VMX_CAP_PROCBASED, // HV_VMX_CAP_PROCBASED
            PROCBASED_CTLS_ONE_SETTING,               // PROCBASED_CTLS_ONE_SETTING
            PROCBASED_CTLS_ZERO_SETTING,              // PROCBASED_CTLS_ZERO_SETTING
            &mut procbased_ctls,
        )
        .context(
            "vmx_init: processor does not support desired primary 
                    processor-based controls",
        )?;

        // Clear the processor-based ctl bits that are set on demand
        procbased_ctls &= !PROCBASED_CTLS_WINDOW_SETTING; // PROCBASED_CTLS_WINDOW_SETTING

        // Check support for secondary processor-based VM-execution controls
        // xhyve: src/vmm/intel/vmx.c:617
        Self::vmx_set_ctlreg(
            vcpu,
            VMCS_SEC_PROC_BASED_CTLS,
            hv_vmx_capability_t_HV_VMX_CAP_PROCBASED2,
            PROCBASED_CTLS2_ONE_SETTING,
            PROCBASED_CTLS2_ZERO_SETTING,
            &mut procbased_ctls2,
        )
        .context(
            "vmx_init: processor does not support desired secondary processor-based controls",
        )?;

        // Check support for pin-based VM-execution controls
        // xhyve: src/vmm/intel/vmx.c:627
        Self::vmx_set_ctlreg(
            vcpu,
            VMCS_PIN_BASED_CTLS,
            hv_vmx_capability_t_HV_VMX_CAP_PINBASED,
            PINBASED_CTLS_ONE_SETTING,
            PINBASED_CTLS_ZERO_SETTING,
            &mut pinbased_ctls,
        )
        .context("vmx_init: processor does not support desired pin-based controls")?;

        // Check support for VM-exit controls
        // xhyve: src/vmm/intel/vmx.c:638
        Self::vmx_set_ctlreg(
            vcpu,
            VMCS_EXIT_CTLS,
            hv_vmx_capability_t_HV_VMX_CAP_EXIT,
            VM_EXIT_CTLS_ONE_SETTING,
            VM_EXIT_CTLS_ZERO_SETTING,
            &mut exit_ctls,
        )
        .context("vmx_init: processor does not support desired exit controls")?;

        // Check support for VM-entry controls
        // xhyve: src/vmm/intel/vmx.c:649
        Self::vmx_set_ctlreg(
            vcpu,
            VMCS_ENTRY_CTLS,
            hv_vmx_capability_t_HV_VMX_CAP_ENTRY,
            VM_ENTRY_CTLS_ONE_SETTING,
            VM_ENTRY_CTLS_ZERO_SETTING,
            &mut self.state[vcpu.id() as usize].entry_ctls,
        )
        .context("vmx_init: processor does not support desired entry controls")?;

        // xhyve: src/vmm/intel/vmx.c:658
        vcpu.write_vmcs(VMCS_PIN_BASED_CTLS, pinbased_ctls);
        vcpu.write_vmcs(VMCS_PRI_PROC_BASED_CTLS, procbased_ctls);
        vcpu.write_vmcs(VMCS_SEC_PROC_BASED_CTLS, procbased_ctls2);
        vcpu.write_vmcs(VMCS_EXIT_CTLS, exit_ctls);
        vcpu.write_vmcs(VMCS_ENTRY_CTLS, self.state[vcpu.id() as usize].entry_ctls);

        // exception bitmap
        // xhyve: src/vmm/intel/vmx.c:665
        let exc_bitmap = if vcpu_trace_exceptions() {
            0xffffffff
        } else {
            1 << IDT_MC
        };

        vcpu.write_vmcs(VMCS_EXCEPTION_BITMAP, exc_bitmap);

        // xhyve: src/vmm/intel/vmx.c:672
        self.cap[vcpu.id() as usize].set = 0;
        self.cap[vcpu.id() as usize].proc_ctls = procbased_ctls;
        self.cap[vcpu.id() as usize].proc_ctls2 = procbased_ctls2;
        self.state[vcpu.id() as usize].nextrip = !0u64;

        // Set up the CR0/4 shadows, and init the read shadow
        // to the power-on register value from the Intel Sys Arch.
        //  CR0 - 0x60000010
        //  CR4 - 0
        //
        // xhyve: src/vmm/intel/vmx.c:677
        self.vmx_setup_cr0_shadow(vcpu, 0x60000010)
            .context("vmx_setup_cr0_shadow")?;
        self.vmx_setup_cr4_shadow(vcpu, 0)
            .context("vmx_setup_cr4_shadow")?;

        Ok(())
    }

    // xhyve: src/vmm/intel/vmx_msr.c:206
    fn vmx_msr_guest_init(&mut self, vcpu: &HvfVcpu) -> anyhow::Result<()> {
        vcpu.enable_native_msr(MSR_LSTAR)?;
        vcpu.enable_native_msr(MSR_CSTAR)?;
        vcpu.enable_native_msr(MSR_STAR)?;
        vcpu.enable_native_msr(MSR_SF_MASK)?;
        vcpu.enable_native_msr(MSR_KGSBASE)?;

        // Initialize guest IA32_PAT MSR with default value after reset.
        guest_msrs[IDX_MSR_PAT] = PAT_VALUE(0, PAT_WRITE_BACK)
            | PAT_VALUE(1, PAT_WRITE_THROUGH)
            | PAT_VALUE(2, PAT_UNCACHED)
            | PAT_VALUE(3, PAT_UNCACHEABLE)
            | PAT_VALUE(4, PAT_WRITE_BACK)
            | PAT_VALUE(5, PAT_WRITE_THROUGH)
            | PAT_VALUE(6, PAT_UNCACHED)
            | PAT_VALUE(7, PAT_UNCACHEABLE);

        Ok(())
    }

    // xhyve: src/vmm/intel/vmx.c:550
    fn vmx_setup_cr0_shadow(&mut self, vcpu: &HvfVcpu, init: u32) -> anyhow::Result<()> {
        self.vmx_setup_cr_shadow(vcpu, 0, init)
    }

    // xhyve: src/vmm/intel/vmx.c:551
    fn vmx_setup_cr4_shadow(&mut self, vcpu: &HvfVcpu, init: u32) -> anyhow::Result<()> {
        self.vmx_setup_cr_shadow(vcpu, 4, init)
    }

    // xhyve: src/vmm/intel/vmx.c:522
    fn vmx_setup_cr_shadow(
        &mut self,
        vcpu: &HvfVcpu,
        which: u32,
        initial: u32,
    ) -> anyhow::Result<()> {
        assert!(which == 0 || which == 4);

        let mask_ident;
        let mask_value;
        let shadow_ident;

        if which == 0 {
            mask_ident = VMCS_CR0_MASK;
            mask_value = (self.cr0_ones_mask | self.cr0_zeros_mask) | (CR0_PG | CR0_PE);
            shadow_ident = VMCS_CR0_SHADOW;
        } else {
            mask_ident = VMCS_CR4_MASK;
            mask_value = self.cr4_ones_mask | self.cr4_zeros_mask;
            shadow_ident = VMCS_CR4_SHADOW;
        }

        // TODO
        //     error = vmcs_setreg(vcpuid, VMCS_IDENT(mask_ident), mask_value);
        //     if (error)
        //         return (error);
        //
        //     error = vmcs_setreg(vcpuid, VMCS_IDENT(shadow_ident), initial);
        //     if (error)
        //         return (error);

        Ok(())
    }

    // xhyve: src/vmm/intel/vmx_msr.c:60
    fn vmx_set_ctlreg(
        vcpu: &HvfVcpu,
        vmcs_field: u32,
        cap_field: u32,
        expect_one: u32,
        expect_zero: u32,
        retval: &mut u32,
    ) -> anyhow::Result<()> {
        // We cannot ask the same bit to be set to both `1` and `0`.
        assert_eq!((expect_one ^ expect_zero), (expect_one | expect_zero));

        let cap = vcpu.read_cap(cap_field).unwrap();
        let current = vcpu.read_vmcs(vmcs_field).unwrap() as u32;

        for i in 0..32 {
            let one_allowed = Self::vmx_ctl_allows_one_setting(cap, i);
            let zero_allowed = Self::vmx_ctl_allows_zero_setting(cap, i);

            if zero_allowed && !one_allowed {
                // Case 1: must be zero
                if expect_one & (1 << i) != 0 {
                    anyhow::bail!(
                        "vmx_set_ctlreg: cap_field: {} bit: {} must be zero\n",
                        cap_field,
                        i
                    );
                }

                *retval &= !(1 << i);
            } else if one_allowed && !zero_allowed {
                // Case 2: must be one
                if expect_zero & (1 << i) != 0 {
                    anyhow::bail!(
                        "vmx_set_ctlreg: cap_field: {} bit: {} must be one\n",
                        cap_field,
                        i
                    );
                }

                *retval |= 1 << i;
            } else {
                // Case 3: don't care
                if expect_zero & (1 << i) != 0 {
                    // The value is expected to be zero; use it.
                    *retval &= !(1 << i);
                } else if expect_one & (1 << i) != 0 {
                    // The value is expected to be one; use it.
                    *retval |= 1 << i;
                } else {
                    // Unknown: keep existing value.
                    *retval = (*retval & !(1 << i)) | (current & (1 << i));
                }
            }
        }

        Ok(())
    }

    // xhyve: src/vmm/intel/vmx_msr.c:43
    fn vmx_ctl_allows_one_setting(msr_val: u64, bitpos: u32) -> bool {
        msr_val & (164 << (bitpos + 32)) != 0
    }

    // xhyve: src/vmm/intel/vmx_msr.c:52
    fn vmx_ctl_allows_zero_setting(msr_val: u64, bitpos: u32) -> bool {
        msr_val & (164 << bitpos) != 0
    }
}
