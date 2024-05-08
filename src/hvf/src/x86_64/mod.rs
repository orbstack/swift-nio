// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

#[allow(non_camel_case_types)]
#[allow(improper_ctypes)]
#[allow(dead_code)]
#[allow(non_snake_case)]
#[allow(non_upper_case_globals)]
#[allow(deref_nullptr)]
mod bindings;
mod reg_init;

use arch::x86_64::gdt::{encode_kvm_segment_ar, kvm_segment};
use arch::x86_64::mptable::APIC_DEFAULT_PHYS_BASE;
use arch::x86_64::regs::kvm_sregs;
use arch_gen::x86::msr_index::{
    MSR_MTRRdefType, MSR_IA32_MISC_ENABLE, MSR_IA32_MISC_ENABLE_FAST_STRING, MSR_KERNEL_GS_BASE,
    MSR_SYSCALL_MASK,
};
use bindings::*;
use vm_memory::GuestMemoryMmap;

use core::panic;
use std::arch::asm;
use std::convert::TryInto;
use std::ffi::c_void;
use std::fmt::{Display, Formatter};
use std::sync::atomic::{AtomicIsize, Ordering};
use std::sync::Arc;
use std::thread::Thread;
use std::time::Duration;

use crossbeam_channel::Sender;
use tracing::{debug, error};

/// IA32_MTRR_DEF_TYPE MSR: E (MTRRs enabled) flag, bit 11
const MTRR_ENABLE: u64 = 0x800;
/// Mem type WB
const MTRR_MEM_TYPE_WB: u64 = 0x6;

#[derive(Clone, Debug, thiserror::Error)]
pub enum Error {
    #[error("map memory")]
    MemoryMap,
    #[error("unmap memory")]
    MemoryUnmap,
    #[error("vcpu read capability")]
    VcpuReadCapability,
    #[error("create vcpu")]
    VcpuCreate,
    #[error("vcpu set initial registers")]
    VcpuInitialRegisters,
    #[error("vcpu read register")]
    VcpuReadRegister,
    #[error("vcpu read msr")]
    VcpuReadMsr,
    #[error("vcpu request exit")]
    VcpuRequestExit,
    #[error("vcpu run")]
    VcpuRun,
    #[error("vcpu set pending irq")]
    VcpuSetPendingIrq,
    #[error("vcpu set register")]
    VcpuSetRegister,
    #[error("vcpu set msr")]
    VcpuSetMsr,
    #[error("vcpu set vtimer mask")]
    VcpuSetVtimerMask,
    #[error("vcpu set apic address")]
    VcpuSetApicAddress,
    #[error("vm create")]
    VmCreate,
    #[error("space create")]
    SpaceCreate,
    #[error("init sregs")]
    InitSregs(arch::x86_64::regs::Error),
}

/// Messages for requesting memory maps/unmaps.
pub enum MemoryMapping {
    AddMapping(Sender<bool>, u64, u64, u64),
    RemoveMapping(Sender<bool>, u64, u64),
}

pub enum InterruptType {
    Irq,
    Fiq,
}

pub fn vcpu_request_exit(vcpuid: hv_vcpuid_t) -> Result<(), Error> {
    let mut vcpu = vcpuid;
    let ret = unsafe { hv_vcpu_interrupt(&mut vcpu, 1) };

    if ret != HV_SUCCESS {
        Err(Error::VcpuRequestExit)
    } else {
        Ok(())
    }
}

// pub fn vcpu_set_pending_irq(
//     vcpuid: u64,
//     irq_type: InterruptType,
//     pending: bool,
// ) -> Result<(), Error> {
//     let _type = match irq_type {
//         InterruptType::Irq => hv_interrupt_type_t_HV_INTERRUPT_TYPE_IRQ,
//         InterruptType::Fiq => hv_interrupt_type_t_HV_INTERRUPT_TYPE_FIQ,
//     };

//     let ret = unsafe { hv_vcpu_set_pending_interrupt(vcpuid, _type, pending) };

//     if ret != HV_SUCCESS {
//         Err(Error::VcpuSetPendingIrq)
//     } else {
//         Ok(())
//     }
// }

// pub fn vcpu_set_vtimer_mask(vcpuid: u64, masked: bool) -> Result<(), Error> {
//     let ret = unsafe { hv_vcpu_set_vtimer_mask(vcpuid, masked) };

//     if ret != HV_SUCCESS {
//         Err(Error::VcpuSetVtimerMask)
//     } else {
//         Ok(())
//     }
// }

pub type VcpuId = u32;

pub trait Parkable: Send + Sync {
    fn park(&self) -> Result<(), ParkError>;
    fn unpark(&self);
    fn before_vcpu_run(&self, vcpuid: VcpuId);
    fn register_vcpu(&self, vcpuid: VcpuId, wfe_thread: Thread);
    fn mark_can_no_longer_park(&self);

    fn should_shutdown(&self) -> bool;
    fn flag_for_shutdown_while_parked(&self);
}

#[derive(Debug, Copy, Clone)]
pub enum ParkError {
    CanNoLongerPark,
}

#[derive(Clone, Debug)]
pub struct HvfVm {}

impl HvfVm {
    pub fn new() -> Result<Self, Error> {
        let ret = unsafe { hv_vm_create((HV_VM_DEFAULT | HV_VM_ACCEL_APIC) as u64) };
        if ret != HV_SUCCESS {
            return Err(Error::VmCreate);
        }

        Ok(Self {})
    }

    pub fn map_memory(
        &self,
        host_start_addr: u64,
        guest_start_addr: u64,
        size: u64,
    ) -> Result<(), Error> {
        let ret = unsafe {
            hv_vm_map(
                host_start_addr as *mut core::ffi::c_void,
                guest_start_addr,
                size as usize,
                (HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC).into(),
            )
        };
        if ret != HV_SUCCESS {
            Err(Error::MemoryMap)
        } else {
            Ok(())
        }
    }

    pub fn unmap_memory(&self, guest_start_addr: u64, size: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vm_unmap(guest_start_addr, size as usize) };
        if ret != HV_SUCCESS {
            Err(Error::MemoryUnmap)
        } else {
            Ok(())
        }
    }

    pub fn force_exits(&self, vcpu_ids: &mut Vec<hv_vcpuid_t>) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_interrupt(vcpu_ids.as_mut_ptr(), vcpu_ids.len() as u32) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuRequestExit)
        } else {
            Ok(())
        }
    }

    pub fn destroy(&self) {
        let res = unsafe { hv_vm_destroy() };
        if res != 0 {
            error!("Failed to destroy HVF VM: {res}");
        }
    }
}

#[derive(Debug)]
pub enum VcpuExit<'a> {
    Breakpoint,
    Canceled,
    CpuOn(u64, u64, u64),
    HypervisorCall,
    HypervisorIoCall {
        dev_id: usize,
        args_ptr: usize,
    },
    MmioRead(u64, &'a mut [u8]),
    MmioWrite(u64, &'a [u8]),
    SecureMonitorCall,
    Shutdown,
    SystemRegister {
        sys_reg: u64,
        arg_reg_idx: u32,
        is_read: bool,
    },
    VtimerActivated,
    WaitForEvent,
    WaitForEventExpired,
    WaitForEventTimeout(Duration),
    IoPortWrite(u16, u64),
}

struct MmioRead {
    addr: u64,
    len: usize,
    srt: u32,
}

pub struct HvfVcpu {
    parker: Arc<dyn Parkable>,
    vcpuid: hv_vcpuid_t,
    mmio_buf: [u8; 8],
    pending_mmio_read: Option<MmioRead>,
    pending_advance_rip: bool,
    guest_mem: GuestMemoryMmap,
}

impl HvfVcpu {
    pub fn new(parker: Arc<dyn Parkable>, guest_mem: GuestMemoryMmap) -> Result<Self, Error> {
        let mut vcpuid: hv_vcpuid_t = 0;

        let ret =
            unsafe { hv_vcpu_create(&mut vcpuid, (HV_VCPU_DEFAULT | HV_VCPU_ACCEL_RDPMC) as u64) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuCreate);
        }

        Ok(Self {
            parker,
            vcpuid,
            mmio_buf: [0; 8],
            pending_mmio_read: None,
            pending_advance_rip: false,
            guest_mem,
        })
    }

    pub fn set_initial_state(&self, boot_ip: u64) -> Result<(), Error> {
        // set APIC address
        let ret =
            unsafe { hv_vmx_vcpu_set_apic_address(self.vcpuid, APIC_DEFAULT_PHYS_BASE as u64) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuSetApicAddress);
        }

        debug!("set MSRs");
        for entry in arch::x86_64::msr::create_boot_msr_entries() {
            self.write_msr(entry.index, entry.data)?;
        }

        // HACK
        reg_init::just_initialize_hvf_already(self)?;

        // setup VM registers (imported from BSD's hypervisor)
        // TODO

        // TODO: FPU

        // setup regs
        debug!("set regs");
        self.write_reg(hv_x86_reg_t_HV_X86_RFLAGS, 0x0000_0000_0000_0002u64)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RIP, boot_ip)?;
        self.write_reg(
            hv_x86_reg_t_HV_X86_RSP,
            arch::x86_64::layout::BOOT_STACK_POINTER,
        )?;
        self.write_reg(
            hv_x86_reg_t_HV_X86_RBP,
            arch::x86_64::layout::BOOT_STACK_POINTER,
        )?;
        self.write_reg(
            hv_x86_reg_t_HV_X86_RSI,
            arch::x86_64::layout::ZERO_PAGE_START,
        )?;

        // setup sregs
        // everything gets overridden except cr0, cr3, cr4, efer
        debug!("set sregs");
        let mut sregs: kvm_sregs = kvm_sregs {
            cr0: self.read_reg(hv_x86_reg_t_HV_X86_CR0)?,
            cr3: self.read_reg(hv_x86_reg_t_HV_X86_CR3)?,
            // TODO: this means ENABLE_VMX, and is supposed to be read from msr FIXED0
            // we can do that by reading capabilities: hv_vmx_capability_t_HV_VMX_CAP_CR0_FIXED0, hv_vmx_capability_t_HV_VMX_CAP_CR0_FIXED1, hv_vmx_capability_t_HV_VMX_CAP_CR4_FIXED0, hv_vmx_capability_t_HV_VMX_CAP_CR4_FIXED1
            // see xhyve: vmx_fix_cr0() and vmx_fix_cr4()
            cr4: self.read_reg(hv_x86_reg_t_HV_X86_CR4)? | 0x2000,
            efer: self.read_vmcs(VMCS_GUEST_IA32_EFER)?,
            ..Default::default()
        };
        arch::x86_64::regs::init_sregs(&self.guest_mem, &mut sregs).map_err(Error::InitSregs)?;

        // set sregs
        debug!("set cs...tr");
        self.write_vmcs(
            VMCS_CTRL_VMENTRY_CONTROLS,
            self.read_vmcs(VMCS_CTRL_VMENTRY_CONTROLS)? | (1 << 9), // IA-32e mode guest
        )?;

        // cs, ds, es, fs, gs, ss, tr, ldtr
        self.write_segment(
            &sregs.cs,
            VMCS_GUEST_CS,
            VMCS_GUEST_CS_BASE,
            VMCS_GUEST_CS_LIMIT,
            VMCS_GUEST_CS_AR,
        )?;
        self.write_segment(
            &sregs.ds,
            VMCS_GUEST_DS,
            VMCS_GUEST_DS_BASE,
            VMCS_GUEST_DS_LIMIT,
            VMCS_GUEST_DS_AR,
        )?;
        self.write_segment(
            &sregs.es,
            VMCS_GUEST_ES,
            VMCS_GUEST_ES_BASE,
            VMCS_GUEST_ES_LIMIT,
            VMCS_GUEST_ES_AR,
        )?;
        self.write_segment(
            &sregs.fs,
            VMCS_GUEST_FS,
            VMCS_GUEST_FS_BASE,
            VMCS_GUEST_FS_LIMIT,
            VMCS_GUEST_FS_AR,
        )?;
        self.write_segment(
            &sregs.gs,
            VMCS_GUEST_GS,
            VMCS_GUEST_GS_BASE,
            VMCS_GUEST_GS_LIMIT,
            VMCS_GUEST_GS_AR,
        )?;
        self.write_segment(
            &sregs.ss,
            VMCS_GUEST_SS,
            VMCS_GUEST_SS_BASE,
            VMCS_GUEST_SS_LIMIT,
            VMCS_GUEST_SS_AR,
        )?;
        self.write_segment(
            &sregs.tr,
            VMCS_GUEST_TR,
            VMCS_GUEST_TR_BASE,
            VMCS_GUEST_TR_LIMIT,
            VMCS_GUEST_TR_AR,
        )?;
        self.write_segment(
            &sregs.ldt,
            VMCS_GUEST_LDTR,
            VMCS_GUEST_LDTR_BASE,
            VMCS_GUEST_LDTR_LIMIT,
            VMCS_GUEST_LDTR_AR,
        )?;

        debug!("set gdtr");
        self.write_reg(hv_x86_reg_t_HV_X86_GDT_BASE, sregs.gdt.base)?;
        self.write_reg(hv_x86_reg_t_HV_X86_GDT_LIMIT, sregs.gdt.limit as u64)?;
        debug!("set idtr");
        self.write_reg(hv_x86_reg_t_HV_X86_IDT_BASE, sregs.idt.base)?;
        self.write_reg(hv_x86_reg_t_HV_X86_IDT_LIMIT, sregs.idt.limit as u64)?;
        debug!("set cr0");
        self.write_reg(hv_x86_reg_t_HV_X86_CR0, sregs.cr0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_CR3, sregs.cr3)?;
        self.write_reg(hv_x86_reg_t_HV_X86_CR4, sregs.cr4)?;
        debug!("set efer");
        self.write_vmcs(VMCS_GUEST_IA32_EFER, sregs.efer)?;

        self.write_vmcs(VMCS_CTRL_CR0_SHADOW, sregs.cr0)?;
        self.write_vmcs(VMCS_CTRL_CR4_SHADOW, sregs.cr4)?;

        // Regular MSR stuff
        self.enable_native_msr(HV_MSR_IA32_GS_BASE)?;
        self.enable_native_msr(HV_MSR_IA32_FS_BASE)?;
        self.enable_native_msr(HV_MSR_IA32_SYSENTER_CS)?;
        self.enable_native_msr(HV_MSR_IA32_SYSENTER_ESP)?;
        self.enable_native_msr(HV_MSR_IA32_SYSENTER_EIP)?;
        self.enable_native_msr(HV_MSR_IA32_TSC)?;
        self.enable_native_msr(HV_MSR_IA32_TSC_AUX)?;

        self.enable_native_msr(HV_MSR_IA32_LSTAR)?;
        self.enable_native_msr(HV_MSR_IA32_CSTAR)?;
        self.enable_native_msr(HV_MSR_IA32_STAR)?;
        self.enable_native_msr(MSR_SYSCALL_MASK)?;
        self.enable_native_msr(HV_MSR_IA32_KERNEL_GS_BASE)?;

        reg_init::dump_hvf_params(self);

        Ok(())
    }

    pub fn id(&self) -> VcpuId {
        self.vcpuid as VcpuId
    }

    pub fn read_reg(&self, reg: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_read_register(self.vcpuid, reg, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadRegister)
        } else {
            Ok(val)
        }
    }

    pub fn write_reg(&self, reg: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_write_register(self.vcpuid, reg, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetRegister)
        } else {
            Ok(())
        }
    }

    fn read_msr(&self, msr: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_read_msr(self.vcpuid, msr, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadMsr)
        } else {
            Ok(val)
        }
    }

    fn write_msr(&self, msr: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_write_msr(self.vcpuid, msr, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetMsr)
        } else {
            Ok(())
        }
    }

    fn read_vmcs(&self, field: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vmx_vcpu_read_vmcs(self.vcpuid, field, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadRegister)
        } else {
            Ok(val)
        }
    }

    fn read_cap(&self, field: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;

        let ret = unsafe { hv_vmx_read_capability(field, &mut val) };

        if ret != HV_SUCCESS {
            Err(Error::VcpuReadCapability)
        } else {
            Ok(val)
        }
    }

    fn write_vmcs(&self, field: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vmx_vcpu_write_vmcs(self.vcpuid, field, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetRegister)
        } else {
            Ok(())
        }
    }

    fn write_segment(
        &self,
        segment: &kvm_segment,
        vmcs_selector: u32,
        vmcs_base: u32,
        vmcs_limit: u32,
        vmcs_ar: u32,
    ) -> Result<(), Error> {
        self.write_vmcs(vmcs_selector, segment.selector as u64)?;
        self.write_vmcs(vmcs_base, segment.base)?;
        self.write_vmcs(vmcs_limit, segment.limit as u64)?;
        self.write_vmcs(vmcs_ar, encode_kvm_segment_ar(segment) as u64)?;
        Ok(())
    }

    fn enable_native_msr(&self, msr: u32) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_enable_native_msr(self.vcpuid, msr, true) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetRegister)
        } else {
            Ok(())
        }
    }

    pub fn run(&mut self, pending_irq: bool) -> Result<VcpuExit, Error> {
        self.parker.before_vcpu_run(self.vcpuid as VcpuId);

        if self.parker.should_shutdown() {
            return Ok(VcpuExit::Shutdown);
        }

        // if let Some(mmio_read) = self.pending_mmio_read.take() {
        //     if mmio_read.srt < 31 {
        //         let val = match mmio_read.len {
        //             1 => u8::from_le_bytes(self.mmio_buf[0..1].try_into().unwrap()) as u64,
        //             2 => u16::from_le_bytes(self.mmio_buf[0..2].try_into().unwrap()) as u64,
        //             4 => u32::from_le_bytes(self.mmio_buf[0..4].try_into().unwrap()) as u64,
        //             8 => u64::from_le_bytes(self.mmio_buf[0..8].try_into().unwrap()),
        //             _ => panic!(
        //                 "unsupported mmio pa={} len={}",
        //                 mmio_read.addr, mmio_read.len
        //             ),
        //         };

        //         self.write_raw_reg(hv_reg_t_HV_REG_X0 + mmio_read.srt, val)?;
        //     }
        // }

        if self.pending_advance_rip {
            let rip = self.read_reg(hv_x86_reg_t_HV_X86_RIP)?;
            let instr_len = self.read_vmcs(VMCS_RO_VMEXIT_INSTR_LEN)?;
            self.write_reg(hv_x86_reg_t_HV_X86_RIP, rip + instr_len)?;
            self.pending_advance_rip = false;
        }

        // if pending_irq {
        //     vcpu_set_pending_irq(self.vcpuid, InterruptType::Irq, true)?;
        // }

        let ret = unsafe { hv_vcpu_run_until(self.vcpuid, HV_DEADLINE_FOREVER) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuRun);
        }

        let mut exit_info: hv_vm_exitinfo_t = 0;
        let ret = unsafe { hv_vcpu_exit_info(self.vcpuid, &mut exit_info) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuRun);
        }
        self.dump_vmcs();

        match exit_info {
            hv_vm_exitinfo_t_HV_VM_EXITINFO_VMX => {
                let exit_reason = self.read_vmcs(VMCS_RO_EXIT_REASON)? as u32;
                match exit_reason {
                    VMX_REASON_VMCALL => Ok(VcpuExit::HypervisorCall),
                    VMX_REASON_CPUID => todo!(),
                    // TODO check pending
                    VMX_REASON_HLT => Ok(VcpuExit::WaitForEvent),
                    VMX_REASON_IO => {
                        let qual = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                        let input = (qual & 8) != 0;
                        let port: u16 = ((qual >> 16) & 0xffff) as u16;
                        if port != 12345 {
                            panic!("unexpected port: {port}");
                        }
                        if input {
                            panic!("unexpected input");
                        }

                        let value = self.read_reg(hv_x86_reg_t_HV_X86_RAX)?;

                        self.pending_advance_rip = true;
                        Ok(VcpuExit::IoPortWrite(port, value))
                    }
                    // handled by hv_vcpu_run_until
                    VMX_REASON_IRQ => panic!("unexpected IRQ vmexit on vcpu {}", self.vcpuid),
                    VMX_REASON_TRIPLE_FAULT => panic!("triple fault on vcpu {}", self.vcpuid),
                    //VMX_REASON_EXC_NMI => {}
                    VMX_REASON_EPT_VIOLATION => {
                        let exit_qualific = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                        //let is_write = (exit_qualific & VMCS_EXIT_QUALIFIC_IO_WRITE) != 0;
                        let rip = self.read_reg(hv_x86_reg_t_HV_X86_RIP)?;
                        let instr_len = self.read_vmcs(VMCS_RO_VMEXIT_INSTR_LEN)?;
                        todo!();
                    }
                    VMX_REASON_VMX_TIMER_EXPIRED => Ok(VcpuExit::VtimerActivated),
                    _ => panic!(
                        "unexpected exit reason: vcpuid={} 0x{:x} - qual={:x}",
                        self.vcpuid,
                        exit_reason,
                        self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?
                    ),
                }
            }
            hv_vm_exitinfo_t_HV_VM_EXITINFO_INIT_AP => todo!(),
            hv_vm_exitinfo_t_HV_VM_EXITINFO_STARTUP_AP => todo!(),
            hv_vm_exitinfo_t_HV_VM_EXITINFO_IOAPIC_EOI => todo!(),
            hv_vm_exitinfo_t_HV_VM_EXITINFO_INJECT_EXCP => todo!(),
            hv_vm_exitinfo_t_HV_VM_EXITINFO_SMI => {
                panic!(
                    "unexpected exit info: vcpuid={} 0x{:x}",
                    self.id(),
                    exit_info
                )
            }
            hv_vm_exitinfo_t_HV_VM_EXITINFO_APIC_ACCESS_READ => todo!(),
            _ => panic!("unhandled exit info"),
        }
    }

    pub fn destroy(self) {
        let err = unsafe { hv_vcpu_destroy(self.vcpuid) };
        if err != 0 {
            error!("Failed to destroy vcpu: {err}");
        }
    }

    fn dump_vmcs(&self) {
        println!("--------START---------");
        for (field_name, field_id) in &[
            ("VMCS_VPID", VMCS_VPID),
            (
                "VMCS_CTRL_POSTED_INT_N_VECTOR",
                VMCS_CTRL_POSTED_INT_N_VECTOR,
            ),
            ("VMCS_CTRL_EPTP_INDEX", VMCS_CTRL_EPTP_INDEX),
            ("VMCS_GUEST_ES", VMCS_GUEST_ES),
            ("VMCS_GUEST_CS", VMCS_GUEST_CS),
            ("VMCS_GUEST_SS", VMCS_GUEST_SS),
            ("VMCS_GUEST_DS", VMCS_GUEST_DS),
            ("VMCS_GUEST_FS", VMCS_GUEST_FS),
            ("VMCS_GUEST_GS", VMCS_GUEST_GS),
            ("VMCS_GUEST_LDTR", VMCS_GUEST_LDTR),
            ("VMCS_GUEST_TR", VMCS_GUEST_TR),
            ("VMCS_GUEST_INT_STATUS", VMCS_GUEST_INT_STATUS),
            ("VMCS_GUESTPML_INDEX", VMCS_GUESTPML_INDEX),
            ("VMCS_HOST_ES", VMCS_HOST_ES),
            ("VMCS_HOST_CS", VMCS_HOST_CS),
            ("VMCS_HOST_SS", VMCS_HOST_SS),
            ("VMCS_HOST_DS", VMCS_HOST_DS),
            ("VMCS_HOST_FS", VMCS_HOST_FS),
            ("VMCS_HOST_GS", VMCS_HOST_GS),
            ("VMCS_HOST_TR", VMCS_HOST_TR),
            ("VMCS_CTRL_IO_BITMAP_A", VMCS_CTRL_IO_BITMAP_A),
            ("VMCS_CTRL_IO_BITMAP_B", VMCS_CTRL_IO_BITMAP_B),
            ("VMCS_CTRL_MSR_BITMAPS", VMCS_CTRL_MSR_BITMAPS),
            (
                "VMCS_CTRL_VMEXIT_MSR_STORE_ADDR",
                VMCS_CTRL_VMEXIT_MSR_STORE_ADDR,
            ),
            (
                "VMCS_CTRL_VMEXIT_MSR_LOAD_ADDR",
                VMCS_CTRL_VMEXIT_MSR_LOAD_ADDR,
            ),
            (
                "VMCS_CTRL_VMENTRY_MSR_LOAD_ADDR",
                VMCS_CTRL_VMENTRY_MSR_LOAD_ADDR,
            ),
            ("VMCS_CTRL_EXECUTIVE_VMCS_PTR", VMCS_CTRL_EXECUTIVE_VMCS_PTR),
            ("VMCS_CTRL_PML_ADDR", VMCS_CTRL_PML_ADDR),
            ("VMCS_CTRL_TSC_OFFSET", VMCS_CTRL_TSC_OFFSET),
            ("VMCS_CTRL_VIRTUAL_APIC", VMCS_CTRL_VIRTUAL_APIC),
            ("VMCS_CTRL_APIC_ACCESS", VMCS_CTRL_APIC_ACCESS),
            (
                "VMCS_CTRL_POSTED_INT_DESC_ADDR",
                VMCS_CTRL_POSTED_INT_DESC_ADDR,
            ),
            ("VMCS_CTRL_VMFUNC_CTRL", VMCS_CTRL_VMFUNC_CTRL),
            ("VMCS_CTRL_EPTP", VMCS_CTRL_EPTP),
            ("VMCS_CTRL_EOI_EXIT_BITMAP_0", VMCS_CTRL_EOI_EXIT_BITMAP_0),
            ("VMCS_CTRL_EOI_EXIT_BITMAP_1", VMCS_CTRL_EOI_EXIT_BITMAP_1),
            ("VMCS_CTRL_EOI_EXIT_BITMAP_2", VMCS_CTRL_EOI_EXIT_BITMAP_2),
            ("VMCS_CTRL_EOI_EXIT_BITMAP_3", VMCS_CTRL_EOI_EXIT_BITMAP_3),
            ("VMCS_CTRL_EPTP_LIST_ADDR", VMCS_CTRL_EPTP_LIST_ADDR),
            ("VMCS_CTRL_VMREAD_BITMAP_ADDR", VMCS_CTRL_VMREAD_BITMAP_ADDR),
            (
                "VMCS_CTRL_VMWRITE_BITMAP_ADDR",
                VMCS_CTRL_VMWRITE_BITMAP_ADDR,
            ),
            ("VMCS_CTRL_VIRT_EXC_INFO_ADDR", VMCS_CTRL_VIRT_EXC_INFO_ADDR),
            ("VMCS_CTRL_XSS_EXITING_BITMAP", VMCS_CTRL_XSS_EXITING_BITMAP),
            (
                "VMCS_CTRL_ENCLS_EXITING_BITMAP",
                VMCS_CTRL_ENCLS_EXITING_BITMAP,
            ),
            ("VMCS_CTRL_SPP_TABLE", VMCS_CTRL_SPP_TABLE),
            ("VMCS_CTRL_TSC_MULTIPLIER", VMCS_CTRL_TSC_MULTIPLIER),
            (
                "VMCS_CTRL_ENCLV_EXITING_BITMAP",
                VMCS_CTRL_ENCLV_EXITING_BITMAP,
            ),
            ("VMCS_GUEST_PHYSICAL_ADDRESS", VMCS_GUEST_PHYSICAL_ADDRESS),
            ("VMCS_GUEST_LINK_POINTER", VMCS_GUEST_LINK_POINTER),
            ("VMCS_GUEST_IA32_DEBUGCTL", VMCS_GUEST_IA32_DEBUGCTL),
            ("VMCS_GUEST_IA32_PAT", VMCS_GUEST_IA32_PAT),
            ("VMCS_GUEST_IA32_EFER", VMCS_GUEST_IA32_EFER),
            (
                "VMCS_GUEST_IA32_PERF_GLOBAL_CTRL",
                VMCS_GUEST_IA32_PERF_GLOBAL_CTRL,
            ),
            ("VMCS_GUEST_PDPTE0", VMCS_GUEST_PDPTE0),
            ("VMCS_GUEST_PDPTE1", VMCS_GUEST_PDPTE1),
            ("VMCS_GUEST_PDPTE2", VMCS_GUEST_PDPTE2),
            ("VMCS_GUEST_PDPTE3", VMCS_GUEST_PDPTE3),
            ("VMCS_GUEST_IA32_BNDCFGS", VMCS_GUEST_IA32_BNDCFGS),
            ("VMCS_GUEST_IA32_RTIT_CTL", VMCS_GUEST_IA32_RTIT_CTL),
            ("VMCS_GUEST_IA32_PKRS", VMCS_GUEST_IA32_PKRS),
            ("VMCS_HOST_IA32_PAT", VMCS_HOST_IA32_PAT),
            ("VMCS_HOST_IA32_EFER", VMCS_HOST_IA32_EFER),
            (
                "VMCS_HOST_IA32_PERF_GLOBAL_CTRL",
                VMCS_HOST_IA32_PERF_GLOBAL_CTRL,
            ),
            ("VMCS_HOST_IA32_PKRS", VMCS_HOST_IA32_PKRS),
            ("VMCS_CTRL_PIN_BASED", VMCS_CTRL_PIN_BASED),
            ("VMCS_CTRL_CPU_BASED", VMCS_CTRL_CPU_BASED),
            ("VMCS_CTRL_EXC_BITMAP", VMCS_CTRL_EXC_BITMAP),
            ("VMCS_CTRL_PF_ERROR_MASK", VMCS_CTRL_PF_ERROR_MASK),
            ("VMCS_CTRL_PF_ERROR_MATCH", VMCS_CTRL_PF_ERROR_MATCH),
            ("VMCS_CTRL_CR3_COUNT", VMCS_CTRL_CR3_COUNT),
            ("VMCS_CTRL_VMEXIT_CONTROLS", VMCS_CTRL_VMEXIT_CONTROLS),
            (
                "VMCS_CTRL_VMEXIT_MSR_STORE_COUNT",
                VMCS_CTRL_VMEXIT_MSR_STORE_COUNT,
            ),
            (
                "VMCS_CTRL_VMEXIT_MSR_LOAD_COUNT",
                VMCS_CTRL_VMEXIT_MSR_LOAD_COUNT,
            ),
            ("VMCS_CTRL_VMENTRY_CONTROLS", VMCS_CTRL_VMENTRY_CONTROLS),
            (
                "VMCS_CTRL_VMENTRY_MSR_LOAD_COUNT",
                VMCS_CTRL_VMENTRY_MSR_LOAD_COUNT,
            ),
            ("VMCS_CTRL_VMENTRY_IRQ_INFO", VMCS_CTRL_VMENTRY_IRQ_INFO),
            ("VMCS_CTRL_VMENTRY_EXC_ERROR", VMCS_CTRL_VMENTRY_EXC_ERROR),
            ("VMCS_CTRL_VMENTRY_INSTR_LEN", VMCS_CTRL_VMENTRY_INSTR_LEN),
            ("VMCS_CTRL_TPR_THRESHOLD", VMCS_CTRL_TPR_THRESHOLD),
            ("VMCS_CTRL_CPU_BASED2", VMCS_CTRL_CPU_BASED2),
            ("VMCS_CTRL_PLE_GAP", VMCS_CTRL_PLE_GAP),
            ("VMCS_CTRL_PLE_WINDOW", VMCS_CTRL_PLE_WINDOW),
            // ("VMCS_RO_INSTR_ERROR", VMCS_RO_INSTR_ERROR),
            // ("VMCS_RO_EXIT_REASON", VMCS_RO_EXIT_REASON),
            // ("VMCS_RO_VMEXIT_IRQ_INFO", VMCS_RO_VMEXIT_IRQ_INFO),
            // ("VMCS_RO_VMEXIT_IRQ_ERROR", VMCS_RO_VMEXIT_IRQ_ERROR),
            // ("VMCS_RO_IDT_VECTOR_INFO", VMCS_RO_IDT_VECTOR_INFO),
            // ("VMCS_RO_IDT_VECTOR_ERROR", VMCS_RO_IDT_VECTOR_ERROR),
            // ("VMCS_RO_VMEXIT_INSTR_LEN", VMCS_RO_VMEXIT_INSTR_LEN),
            // ("VMCS_RO_VMX_INSTR_INFO", VMCS_RO_VMX_INSTR_INFO),
            ("VMCS_GUEST_ES_LIMIT", VMCS_GUEST_ES_LIMIT),
            ("VMCS_GUEST_CS_LIMIT", VMCS_GUEST_CS_LIMIT),
            ("VMCS_GUEST_SS_LIMIT", VMCS_GUEST_SS_LIMIT),
            ("VMCS_GUEST_DS_LIMIT", VMCS_GUEST_DS_LIMIT),
            ("VMCS_GUEST_FS_LIMIT", VMCS_GUEST_FS_LIMIT),
            ("VMCS_GUEST_GS_LIMIT", VMCS_GUEST_GS_LIMIT),
            ("VMCS_GUEST_LDTR_LIMIT", VMCS_GUEST_LDTR_LIMIT),
            ("VMCS_GUEST_TR_LIMIT", VMCS_GUEST_TR_LIMIT),
            ("VMCS_GUEST_GDTR_LIMIT", VMCS_GUEST_GDTR_LIMIT),
            ("VMCS_GUEST_IDTR_LIMIT", VMCS_GUEST_IDTR_LIMIT),
            ("VMCS_GUEST_ES_AR", VMCS_GUEST_ES_AR),
            ("VMCS_GUEST_CS_AR", VMCS_GUEST_CS_AR),
            ("VMCS_GUEST_SS_AR", VMCS_GUEST_SS_AR),
            ("VMCS_GUEST_DS_AR", VMCS_GUEST_DS_AR),
            ("VMCS_GUEST_FS_AR", VMCS_GUEST_FS_AR),
            ("VMCS_GUEST_GS_AR", VMCS_GUEST_GS_AR),
            ("VMCS_GUEST_LDTR_AR", VMCS_GUEST_LDTR_AR),
            ("VMCS_GUEST_TR_AR", VMCS_GUEST_TR_AR),
            ("VMCS_GUEST_INTERRUPTIBILITY", VMCS_GUEST_INTERRUPTIBILITY),
            ("VMCS_GUEST_IGNORE_IRQ", VMCS_GUEST_IGNORE_IRQ),
            ("VMCS_GUEST_ACTIVITY_STATE", VMCS_GUEST_ACTIVITY_STATE),
            ("VMCS_GUEST_SMBASE", VMCS_GUEST_SMBASE),
            ("VMCS_GUEST_IA32_SYSENTER_CS", VMCS_GUEST_IA32_SYSENTER_CS),
            ("VMCS_GUEST_VMX_TIMER_VALUE", VMCS_GUEST_VMX_TIMER_VALUE),
            ("VMCS_HOST_IA32_SYSENTER_CS", VMCS_HOST_IA32_SYSENTER_CS),
            ("VMCS_CTRL_CR0_MASK", VMCS_CTRL_CR0_MASK),
            ("VMCS_CTRL_CR4_MASK", VMCS_CTRL_CR4_MASK),
            ("VMCS_CTRL_CR0_SHADOW", VMCS_CTRL_CR0_SHADOW),
            ("VMCS_CTRL_CR4_SHADOW", VMCS_CTRL_CR4_SHADOW),
            ("VMCS_CTRL_CR3_VALUE0", VMCS_CTRL_CR3_VALUE0),
            ("VMCS_CTRL_CR3_VALUE1", VMCS_CTRL_CR3_VALUE1),
            ("VMCS_CTRL_CR3_VALUE2", VMCS_CTRL_CR3_VALUE2),
            ("VMCS_CTRL_CR3_VALUE3", VMCS_CTRL_CR3_VALUE3),
            // ("VMCS_RO_EXIT_QUALIFIC", VMCS_RO_EXIT_QUALIFIC),
            // ("VMCS_RO_IO_RCX", VMCS_RO_IO_RCX),
            // ("VMCS_RO_IO_RSI", VMCS_RO_IO_RSI),
            // ("VMCS_RO_IO_RDI", VMCS_RO_IO_RDI),
            // ("VMCS_RO_IO_RIP", VMCS_RO_IO_RIP),
            // ("VMCS_RO_GUEST_LIN_ADDR", VMCS_RO_GUEST_LIN_ADDR),
            ("VMCS_GUEST_CR0", VMCS_GUEST_CR0),
            ("VMCS_GUEST_CR3", VMCS_GUEST_CR3),
            ("VMCS_GUEST_CR4", VMCS_GUEST_CR4),
            ("VMCS_GUEST_ES_BASE", VMCS_GUEST_ES_BASE),
            ("VMCS_GUEST_CS_BASE", VMCS_GUEST_CS_BASE),
            ("VMCS_GUEST_SS_BASE", VMCS_GUEST_SS_BASE),
            ("VMCS_GUEST_DS_BASE", VMCS_GUEST_DS_BASE),
            ("VMCS_GUEST_FS_BASE", VMCS_GUEST_FS_BASE),
            ("VMCS_GUEST_GS_BASE", VMCS_GUEST_GS_BASE),
            ("VMCS_GUEST_LDTR_BASE", VMCS_GUEST_LDTR_BASE),
            ("VMCS_GUEST_TR_BASE", VMCS_GUEST_TR_BASE),
            ("VMCS_GUEST_GDTR_BASE", VMCS_GUEST_GDTR_BASE),
            ("VMCS_GUEST_IDTR_BASE", VMCS_GUEST_IDTR_BASE),
            ("VMCS_GUEST_DR7", VMCS_GUEST_DR7),
            ("VMCS_GUEST_RSP", VMCS_GUEST_RSP),
            ("VMCS_GUEST_RIP", VMCS_GUEST_RIP),
            ("VMCS_GUEST_RFLAGS", VMCS_GUEST_RFLAGS),
            ("VMCS_GUEST_DEBUG_EXC", VMCS_GUEST_DEBUG_EXC),
            ("VMCS_GUEST_SYSENTER_ESP", VMCS_GUEST_SYSENTER_ESP),
            ("VMCS_GUEST_SYSENTER_EIP", VMCS_GUEST_SYSENTER_EIP),
            ("VMCS_GUEST_IA32_S_CET", VMCS_GUEST_IA32_S_CET),
            ("VMCS_GUEST_SSP", VMCS_GUEST_SSP),
            (
                "VMCS_GUEST_IA32_INTR_SSP_TABLE_ADDR",
                VMCS_GUEST_IA32_INTR_SSP_TABLE_ADDR,
            ),
            ("VMCS_HOST_CR0", VMCS_HOST_CR0),
            ("VMCS_HOST_CR3", VMCS_HOST_CR3),
            ("VMCS_HOST_CR4", VMCS_HOST_CR4),
            ("VMCS_HOST_FS_BASE", VMCS_HOST_FS_BASE),
            ("VMCS_HOST_GS_BASE", VMCS_HOST_GS_BASE),
            ("VMCS_HOST_TR_BASE", VMCS_HOST_TR_BASE),
            ("VMCS_HOST_GDTR_BASE", VMCS_HOST_GDTR_BASE),
            ("VMCS_HOST_IDTR_BASE", VMCS_HOST_IDTR_BASE),
            ("VMCS_HOST_IA32_SYSENTER_ESP", VMCS_HOST_IA32_SYSENTER_ESP),
            ("VMCS_HOST_IA32_SYSENTER_EIP", VMCS_HOST_IA32_SYSENTER_EIP),
            ("VMCS_HOST_RSP", VMCS_HOST_RSP),
            ("VMCS_HOST_RIP", VMCS_HOST_RIP),
            ("VMCS_HOST_IA32_S_CET", VMCS_HOST_IA32_S_CET),
            ("VMCS_HOST_SSP", VMCS_HOST_SSP),
            (
                "VMCS_HOST_IA32_INTR_SSP_TABLE_ADDR",
                VMCS_HOST_IA32_INTR_SSP_TABLE_ADDR,
            ),
        ] {
            let value = match self.read_vmcs(*field_id) {
                Ok(value) => value,
                Err(e) => {
                    //error!("Failed to read field {field_name}: {e}");
                    continue;
                }
            };
            println!("{field_name} = {value:016x}");
        }
        println!("--------END---------");
    }
}
