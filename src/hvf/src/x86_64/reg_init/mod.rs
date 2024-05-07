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

use crate::{
    x86_64::{
        hv_x86_reg_t_HV_X86_CR2, hv_x86_reg_t_HV_X86_R10, hv_x86_reg_t_HV_X86_R11,
        hv_x86_reg_t_HV_X86_R12, hv_x86_reg_t_HV_X86_R14, hv_x86_reg_t_HV_X86_R15,
        hv_x86_reg_t_HV_X86_R8, hv_x86_reg_t_HV_X86_R9, hv_x86_reg_t_HV_X86_RAX,
        hv_x86_reg_t_HV_X86_RBP, hv_x86_reg_t_HV_X86_RBX, hv_x86_reg_t_HV_X86_RCX,
        hv_x86_reg_t_HV_X86_RDI, hv_x86_reg_t_HV_X86_RDX, hv_x86_reg_t_HV_X86_RFLAGS,
        hv_x86_reg_t_HV_X86_RIP, hv_x86_reg_t_HV_X86_RSI, hv_x86_reg_t_HV_X86_RSP,
    },
    HvfVcpu,
};

use super::{
    hv_vmx_capability_t_HV_VMX_CAP_ENTRY, hv_vmx_capability_t_HV_VMX_CAP_EXIT,
    hv_vmx_capability_t_HV_VMX_CAP_PINBASED, hv_vmx_capability_t_HV_VMX_CAP_PROCBASED,
    hv_vmx_capability_t_HV_VMX_CAP_PROCBASED2, VMCS_CTRL_CR0_SHADOW,
};

mod constants;
use constants::*;

const VCPU_TRACE_EXCEPTIONS: bool = false;

#[derive(Default)]
struct VmSetupState {
    cap_halt_exit: u32,
    cap_monitor_trap: u32,
    cap_pause_exit: u32,
    cr0_ones_mask: u32,
    cr4_ones_mask: u32,
    cr0_zeros_mask: u32,
    cr4_zeros_mask: u32,
}

impl VmSetupState {
    // xhyve: src/vmm/intel/vmx.c:468
    fn vmx_init(&mut self) {
        // Check support for optional features by testing them as individual bits
        self.cap_halt_exit = 1;
        self.cap_monitor_trap = 1;
        self.cap_pause_exit = 1;

        self.cr0_ones_mask = 0;
        self.cr4_ones_mask = 0;
        self.cr0_zeros_mask = 0;
        self.cr4_zeros_mask = 0;

        self.cr0_ones_mask |= CR0_NE | CR0_ET;
        self.cr0_zeros_mask |= CR0_NW | CR0_CD;
        self.cr4_ones_mask = 0x2000;
    }

    // xhyve: src/vmm/intel/vmx.c:567
    fn vmx_vcpu_init(&mut self, vcpu: &HvfVcpu) -> anyhow::Result<()> {
        // xhyve: src/vmm/intel/vmx.c:584
        vcpu.enable_native_msr(MSR_GSBASE).unwrap();
        vcpu.enable_native_msr(MSR_FSBASE).unwrap();
        vcpu.enable_native_msr(MSR_SYSENTER_CS_MSR).unwrap();
        vcpu.enable_native_msr(MSR_SYSENTER_ESP_MSR).unwrap();
        vcpu.enable_native_msr(MSR_SYSENTER_EIP_MSR).unwrap();
        vcpu.enable_native_msr(MSR_TSC).unwrap();
        vcpu.enable_native_msr(MSR_IA32_TSC_AUX).unwrap();

        // xhyve: src/vmm/intel/vmx.c:595
        self.vmx_msr_guest_init(vcpu).unwrap();

        // xhyve: src/vmm/intel/vmx.c:597
        let mut pinbased_ctls = 0u32;
        let mut procbased_ctls = 0u32;
        let mut procbased_ctls2 = 0u32;
        let mut exit_ctls = 0u32;

        let mut hack_entry_ctls = 0;
        // self.state[vcpu.id() as usize].entry_ctls = 0;

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
        )
        .unwrap();

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
        .context("vmx_init: processor does not support desired secondary processor-based controls")
        .unwrap();

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
        .context("vmx_init: processor does not support desired pin-based controls")
        .unwrap();

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
        .context("vmx_init: processor does not support desired exit controls")
        .unwrap();

        // Check support for VM-entry controls
        // xhyve: src/vmm/intel/vmx.c:649
        Self::vmx_set_ctlreg(
            vcpu,
            VMCS_ENTRY_CTLS,
            hv_vmx_capability_t_HV_VMX_CAP_ENTRY,
            VM_ENTRY_CTLS_ONE_SETTING,
            VM_ENTRY_CTLS_ZERO_SETTING,
            &mut hack_entry_ctls, // &mut self.state[vcpu.id() as usize].entry_ctls,
        )
        .context("vmx_init: processor does not support desired entry controls")
        .unwrap();

        // xhyve: src/vmm/intel/vmx.c:658
        vcpu.write_vmcs(VMCS_PIN_BASED_CTLS, pinbased_ctls as u64)
            .unwrap();
        vcpu.write_vmcs(VMCS_PRI_PROC_BASED_CTLS, procbased_ctls as u64)
            .unwrap();
        vcpu.write_vmcs(VMCS_SEC_PROC_BASED_CTLS, procbased_ctls2 as u64)
            .unwrap();
        vcpu.write_vmcs(VMCS_EXIT_CTLS, exit_ctls as u64).unwrap();
        vcpu.write_vmcs(
            VMCS_ENTRY_CTLS,
            hack_entry_ctls as u64, //self.state[vcpu.id() as usize].entry_ctls as u64,
        )
        .unwrap();

        // exception bitmap
        // xhyve: src/vmm/intel/vmx.c:665
        let exc_bitmap = if VCPU_TRACE_EXCEPTIONS {
            0xffffffff
        } else {
            1 << IDT_MC
        };

        vcpu.write_vmcs(VMCS_EXCEPTION_BITMAP, exc_bitmap).unwrap();

        // xhyve: src/vmm/intel/vmx.c:672
        // self.cap[vcpu.id() as usize].set = 0;
        // self.cap[vcpu.id() as usize].proc_ctls = procbased_ctls;
        // self.cap[vcpu.id() as usize].proc_ctls2 = procbased_ctls2;
        // self.state[vcpu.id() as usize].nextrip = !0u64;

        // Set up the CR0/4 shadows, and init the read shadow
        // to the power-on register value from the Intel Sys Arch.
        //  CR0 - 0x60000010
        //  CR4 - 0
        //
        // xhyve: src/vmm/intel/vmx.c:677
        self.vmx_setup_cr0_shadow(vcpu, 0x60000010)
            .context("vmx_setup_cr0_shadow")
            .unwrap();

        self.vmx_setup_cr4_shadow(vcpu, 0)
            .context("vmx_setup_cr4_shadow")
            .unwrap();

        Ok(())
    }

    // xhyve: src/vmm/intel/vmx_msr.c:206
    fn vmx_msr_guest_init(&mut self, vcpu: &HvfVcpu) -> anyhow::Result<()> {
        vcpu.enable_native_msr(MSR_LSTAR).unwrap();
        vcpu.enable_native_msr(MSR_CSTAR).unwrap();
        vcpu.enable_native_msr(MSR_STAR).unwrap();
        vcpu.enable_native_msr(MSR_SF_MASK).unwrap();
        vcpu.enable_native_msr(MSR_KGSBASE).unwrap();

        // Initialize guest IA32_PAT MSR with default value after reset.
        // guest_msrs[IDX_MSR_PAT] = Self::pat_value(0, PAT_WRITE_BACK)
        //     | Self::pat_value(1, PAT_WRITE_THROUGH)
        //     | Self::pat_value(2, PAT_UNCACHED)
        //     | Self::pat_value(3, PAT_UNCACHEABLE)
        //     | Self::pat_value(4, PAT_WRITE_BACK)
        //     | Self::pat_value(5, PAT_WRITE_THROUGH)
        //     | Self::pat_value(6, PAT_UNCACHED)
        //     | Self::pat_value(7, PAT_UNCACHEABLE);

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

        vcpu.write_vmcs(
            mask_ident,
            self.vmcs_fix_regval(mask_ident, mask_value as u64),
        )
        .unwrap();

        vcpu.write_vmcs(
            shadow_ident,
            self.vmcs_fix_regval(shadow_ident, initial as u64),
        )
        .unwrap();

        Ok(())
    }

    // xhyve: src/vmm/intel/vmcs.c:36
    fn vmcs_fix_regval(&mut self, encoding: u32, val: u64) -> u64 {
        match encoding {
            VMCS_GUEST_CR0 => self.vmx_fix_cr0(val),
            VMCS_GUEST_CR4 => self.vmx_fix_cr4(val),
            _ => val,
        }
    }

    // xhyve: src/vmm/intel/vmx.c:450
    fn vmx_fix_cr0(&mut self, cr0: u64) -> u64 {
        (cr0 | self.cr0_ones_mask as u64) & !(self.cr0_zeros_mask as u64)
    }

    // xhyve: src/vmm/intel/vmx.c:456
    fn vmx_fix_cr4(&mut self, cr4: u64) -> u64 {
        (cr4 | self.cr4_ones_mask as u64) & !(self.cr4_zeros_mask as u64)
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
        msr_val & (1u64 << (bitpos + 32)) != 0
    }

    // xhyve: src/vmm/intel/vmx_msr.c:52
    fn vmx_ctl_allows_zero_setting(msr_val: u64, bitpos: u32) -> bool {
        msr_val & (1u64 << bitpos) == 0
    }

    // xhyve: include/xhyve/support/specialreg.h:535
    fn pat_value(i: u32, m: u32) -> u64 {
        (m << (8 * i)) as u64
    }

    // xhyve: include/xhyve/support/specialreg.h:536
    fn pat_mask(i: u32) -> u64 {
        Self::pat_value(i, 0xFF)
    }

    // xhyve: src/vmm/intel/vmx.c:164
    fn hvdump(vcpu: &HvfVcpu) {
        println!(
            "VMCS_PIN_BASED_CTLS:           {:#018x}",
            vcpu.read_vmcs(VMCS_PIN_BASED_CTLS).unwrap(),
        );
        println!(
            "VMCS_PRI_PROC_BASED_CTLS:      {:#018x}",
            vcpu.read_vmcs(VMCS_PRI_PROC_BASED_CTLS).unwrap(),
        );
        println!(
            "VMCS_SEC_PROC_BASED_CTLS:      {:#018x}",
            vcpu.read_vmcs(VMCS_SEC_PROC_BASED_CTLS).unwrap(),
        );
        println!(
            "VMCS_ENTRY_CTLS:               {:#018x}",
            vcpu.read_vmcs(VMCS_ENTRY_CTLS).unwrap(),
        );
        println!(
            "VMCS_EXCEPTION_BITMAP:         {:#018x}",
            vcpu.read_vmcs(VMCS_EXCEPTION_BITMAP).unwrap(),
        );
        println!(
            "VMCS_CR0_MASK:                 {:#018x}",
            vcpu.read_vmcs(VMCS_CR0_MASK).unwrap(),
        );
        println!(
            "VMCS_CR0_SHADOW:               {:#018x}",
            vcpu.read_vmcs(VMCS_CR0_SHADOW).unwrap(),
        );
        println!(
            "VMCS_CR4_MASK:                 {:#018x}",
            vcpu.read_vmcs(VMCS_CR4_MASK).unwrap(),
        );
        println!(
            "VMCS_CR4_SHADOW:               {:#018x}",
            vcpu.read_vmcs(VMCS_CR4_SHADOW).unwrap(),
        );
        println!(
            "VMCS_GUEST_CS_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CS_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_CS_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CS_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_CS_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CS_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_CS_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CS_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_DS_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_DS_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_DS_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_DS_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_DS_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_DS_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_DS_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_DS_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_ES_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_ES_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_ES_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_ES_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_ES_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_ES_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_ES_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_ES_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_FS_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_FS_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_FS_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_FS_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_FS_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_FS_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_FS_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_FS_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_GS_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_GS_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_GS_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_GS_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_GS_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_GS_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_GS_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_GS_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_SS_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_SS_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_SS_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_SS_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_SS_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_SS_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_SS_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_SS_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_LDTR_SELECTOR:      {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_LDTR_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_LDTR_LIMIT:         {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_LDTR_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_LDTR_ACCESS_RIGHTS: {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_LDTR_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_LDTR_BASE:          {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_LDTR_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_TR_SELECTOR:        {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_TR_SELECTOR).unwrap(),
        );
        println!(
            "VMCS_GUEST_TR_LIMIT:           {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_TR_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_TR_ACCESS_RIGHTS:   {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_TR_ACCESS_RIGHTS).unwrap(),
        );
        println!(
            "VMCS_GUEST_TR_BASE:            {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_TR_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_GDTR_LIMIT:         {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_GDTR_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_GDTR_BASE:          {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_GDTR_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_IDTR_LIMIT:         {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_LDTR_LIMIT).unwrap(),
        );
        println!(
            "VMCS_GUEST_IDTR_BASE:          {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_LDTR_BASE).unwrap(),
        );
        println!(
            "VMCS_GUEST_CR0:                {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CR0).unwrap(),
        );
        println!(
            "VMCS_GUEST_CR3:                {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CR3).unwrap(),
        );
        println!(
            "VMCS_GUEST_CR4:                {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_CR4).unwrap(),
        );
        println!(
            "VMCS_GUEST_IA32_EFER:          {:#018x}",
            vcpu.read_vmcs(VMCS_GUEST_IA32_EFER).unwrap(),
        );
        println!();
        println!(
            "rip: {:#018x} rfl: {:#018x} cr2: {:#018x}",
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RIP).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RFLAGS).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_CR2).unwrap(),
        );
        println!(
            "rax: {:#018x} rbx: {:#018x} rcx: {:#018x} rdx: {:#018x}",
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RAX).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RBX).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RCX).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RDX).unwrap(),
        );
        println!(
            "rsi: {:#018x} rdi: {:#018x} rbp: {:#018x} rsp: {:#018x}",
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RSI).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RDI).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RBP).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_RSP).unwrap(),
        );
        println!(
            "r8:  {:#018x} r9:  {:#018x} r10: {:#018x} r11: {:#018x}",
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R8).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R9).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R10).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R11).unwrap(),
        );
        println!(
            "r12: {:#018x} r13: {:#018x} r14: {:#018x} r15: {:#018x}",
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R12).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R12).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R14).unwrap(),
            vcpu.read_reg(hv_x86_reg_t_HV_X86_R15).unwrap(),
        );
    }
}

pub fn just_initialize_hvf_already(vcpu: &HvfVcpu) -> Result<(), crate::Error> {
    let mut state = VmSetupState::default();
    state.vmx_init();
    state
        .vmx_vcpu_init(vcpu)
        // TODO: Get better errors!
        .map_err(|_| crate::Error::VcpuCreate)
        .unwrap();

    Ok(())
}

pub fn dump_hvf_params(vcpu: &HvfVcpu) {
    VmSetupState::hvdump(vcpu)
}
