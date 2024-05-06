// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

#[allow(non_camel_case_types)]
#[allow(improper_ctypes)]
#[allow(dead_code)]
#[allow(non_snake_case)]
#[allow(non_upper_case_globals)]
#[allow(deref_nullptr)]
mod bindings;
mod bsd;

use arch::x86_64::gdt::encode_kvm_segment;
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
            cr4: self.read_reg(hv_x86_reg_t_HV_X86_CR4)?,
            efer: self.read_vmcs(VMCS_GUEST_IA32_EFER)?,
            ..Default::default()
        };
        arch::x86_64::regs::init_sregs(&self.guest_mem, &mut sregs).map_err(Error::InitSregs)?;
        // set sregs
        debug!("set cs...tr");
        self.write_vmcs(
            VMCS_CTRL_VMENTRY_CONTROLS,
            self.read_vmcs(VMCS_CTRL_VMENTRY_CONTROLS)?, // IA-32e mode guest
                                                         // | (1<<9)
                                                         // // Load IA32_EFER
                                                         // | (1<<15),
        )?;
        self.write_reg(hv_x86_reg_t_HV_X86_CS, encode_kvm_segment(&sregs.cs))?;
        self.write_vmcs(VMCS_GUEST_CS_BASE, sregs.cs.base)?;
        self.write_vmcs(VMCS_GUEST_CS_LIMIT, sregs.cs.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_CS_AR, sregs.cs.ar)?;
        self.write_reg(hv_x86_reg_t_HV_X86_DS, encode_kvm_segment(&sregs.ds))?;
        self.write_vmcs(VMCS_GUEST_DS_BASE, sregs.ds.base)?;
        self.write_vmcs(VMCS_GUEST_DS_LIMIT, sregs.ds.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_DS_AR, sregs.ds.ar)?;
        self.write_reg(hv_x86_reg_t_HV_X86_ES, encode_kvm_segment(&sregs.es))?;
        self.write_vmcs(VMCS_GUEST_ES_BASE, sregs.es.base)?;
        self.write_vmcs(VMCS_GUEST_ES_LIMIT, sregs.es.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_ES_AR, sregs.es.ar)?;
        self.write_reg(hv_x86_reg_t_HV_X86_FS, encode_kvm_segment(&sregs.fs))?;
        self.write_vmcs(VMCS_GUEST_FS_BASE, sregs.fs.base)?;
        self.write_vmcs(VMCS_GUEST_FS_LIMIT, sregs.fs.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_FS_AR, sregs.fs.ar)?;
        self.write_reg(hv_x86_reg_t_HV_X86_GS, encode_kvm_segment(&sregs.gs))?;
        self.write_vmcs(VMCS_GUEST_GS_BASE, sregs.gs.base)?;
        self.write_vmcs(VMCS_GUEST_GS_LIMIT, sregs.gs.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_GS_AR, sregs.gs.ar)?;
        self.write_reg(hv_x86_reg_t_HV_X86_SS, encode_kvm_segment(&sregs.ss))?;
        self.write_vmcs(VMCS_GUEST_SS_BASE, sregs.ss.base)?;
        self.write_vmcs(VMCS_GUEST_SS_LIMIT, sregs.ss.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_SS_AR, sregs.ss.ar)?;
        self.write_reg(hv_x86_reg_t_HV_X86_TR, encode_kvm_segment(&sregs.tr))?;
        self.write_vmcs(VMCS_GUEST_TR_BASE, sregs.tr.base)?;
        self.write_vmcs(VMCS_GUEST_TR_LIMIT, sregs.tr.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_TR_AR, sregs.tr.ar)?;
        debug!("set ldtr");
        self.write_reg(hv_x86_reg_t_HV_X86_LDTR, encode_kvm_segment(&sregs.ldt))?;
        self.write_vmcs(VMCS_GUEST_LDTR_BASE, sregs.ldt.base)?;
        self.write_vmcs(VMCS_GUEST_LDTR_LIMIT, sregs.ldt.limit as u64)?;
        //self.write_vmcs(VMCS_GUEST_LDTR_AR, sregs.ldt.ar)?;
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
        // causes vcpu run = HV_ERROR
        // self.write_vmcs(VMCS_GUEST_IA32_EFER, sregs.efer)?;

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

    pub fn read_msr(&self, msr: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_read_msr(self.vcpuid, msr, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadMsr)
        } else {
            Ok(val)
        }
    }

    pub fn write_msr(&self, msr: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_write_msr(self.vcpuid, msr, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetMsr)
        } else {
            Ok(())
        }
    }

    pub fn read_vmcs(&self, field: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vmx_vcpu_read_vmcs(self.vcpuid, field, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadRegister)
        } else {
            Ok(val)
        }
    }

    pub fn read_cap(&self, field: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;

        let ret = unsafe { hv_vmx_read_capability(field, &mut val) };

        if ret != HV_SUCCESS {
            Err(Error::VcpuReadCapability)
        } else {
            Ok(val)
        }
    }

    pub fn write_vmcs(&self, field: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vmx_vcpu_write_vmcs(self.vcpuid, field, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetRegister)
        } else {
            Ok(())
        }
    }

    pub fn enable_native_msr(&self, msr: u32) -> Result<(), Error> {
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
}
