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
use arch::x86_64::mptable::{APIC_DEFAULT_PHYS_BASE, IO_APIC_DEFAULT_PHYS_BASE};
use arch::x86_64::regs::{kvm_sregs, X86_CR0_PG};
use arch_gen::x86::msr_index::{
    MSR_MTRRcap, MSR_MTRRdefType, MSR_MTRRfix16K_80000, MSR_MTRRfix16K_A0000, MSR_MTRRfix4K_C0000,
    MSR_MTRRfix4K_C8000, MSR_MTRRfix4K_D0000, MSR_MTRRfix4K_D8000, MSR_MTRRfix4K_E0000,
    MSR_MTRRfix4K_E8000, MSR_MTRRfix4K_F0000, MSR_MTRRfix4K_F8000, MSR_MTRRfix64K_00000, EFER_LMA,
    EFER_LME, MSR_DRAM_ENERGY_STATUS, MSR_EFER, MSR_IA32_APERF, MSR_IA32_APICBASE, MSR_IA32_CR_PAT,
    MSR_IA32_FEATURE_CONTROL, MSR_IA32_MISC_ENABLE, MSR_IA32_MISC_ENABLE_FAST_STRING,
    MSR_IA32_MPERF, MSR_IA32_PERF_CAPABILITIES, MSR_IA32_UCODE_REV, MSR_IA32_XSS,
    MSR_KERNEL_GS_BASE, MSR_MISC_FEATURE_ENABLES, MSR_PKG_ENERGY_STATUS,
    MSR_PLATFORM_ENERGY_STATUS, MSR_PLATFORM_INFO, MSR_PP0_ENERGY_STATUS, MSR_PP1_ENERGY_STATUS,
    MSR_PPERF, MSR_RAPL_POWER_UNIT, MSR_SMI_COUNT, MSR_SYSCALL_MASK,
};
use bindings::*;
use iced_x86::{Code, Decoder, DecoderOptions, Instruction, Register};
use vm_memory::{Address, Bytes, GuestAddress, GuestMemoryMmap};

use core::panic;
use std::arch::asm;
use std::arch::x86_64::__cpuid_count;
use std::convert::TryInto;
use std::ffi::c_void;
use std::fmt::{Display, Formatter};
use std::mem::MaybeUninit;
use std::sync::atomic::{AtomicIsize, Ordering};
use std::sync::Arc;
use std::thread::Thread;
use std::time::Duration;

use crossbeam_channel::Sender;
use tracing::{debug, error, trace, warn};

/// IA32_MTRR_DEF_TYPE MSR: E (MTRRs enabled) flag, bit 11
const MTRR_ENABLE: u64 = 0x800;
/// Mem type WB
const MTRR_MEM_TYPE_WB: u64 = 0x6;

const LAPIC_TPR: u32 = 0x80;

const APIC_LVT0: u32 = 0x350;
const APIC_LVT1: u32 = 0x360;
const APIC_MODE_NMI: u32 = 0x4;
const APIC_MODE_EXTINT: u32 = 0x7;

// from xhyve:
// set branch trace disabled(11), PEBS unavailable(12)
const IA32_MISC_ENABLE_VALUE: u64 = 1 | (1 << 11) | (1 << 12);

const VM_INTINFO_VALID: u64 = 0x80000000;
const VM_INTINFO_HWINTR: u64 = 0 << 8;
const VM_INTINFO_NMI: u64 = 2 << 8;
const VM_INTINFO_HWEXCEPTION: u64 = 3 << 8;
const VM_INTINFO_SWINTR: u64 = 4 << 8;

const CPUID_XSTATE: u32 = 0xd;
const CPUID_KVM: u32 = 0x40000000;
const CPUID_KVM_FEATURES: u32 = 0x40000001;

const IA32E_MODE_GUEST: u64 = 1 << 9;

const PROCBASED2_UNRESTRICTED_GUEST: u64 = 1 << 7;

const CR0_PG: u64 = 0x80000000;
const CR0_PE: u64 = 0x00000001;
const CR0_NE: u64 = 0x00000020;

const XSTATE_FLAG_SUPERVISOR: u32 = 1 << 0;
const XSTATE_FLAG_ALIGNED: u32 = 1 << 1;

const MSR_TSX_FORCE_ABORT: u32 = 0x0000010f;
const MSR_IA32_TSX_CTRL: u32 = 0x00000122;

const FEAT_CTL_LOCKED: u64 = 1 << 0;

const IOAPIC_START: u64 = IO_APIC_DEFAULT_PHYS_BASE as u64;
const IOAPIC_END_INCL: u64 = IOAPIC_START + 0x1000 - 1;

const EPT_VIOLATION_DATA_READ: u64 = 1 << 0;
const EPT_VIOLATION_DATA_WRITE: u64 = 1 << 1;
const EPT_VIOLATION_INST_FETCH: u64 = 1 << 2;

const PTE_PRESENT: u64 = 1 << 0;
const PTE_PAGE_SIZE: u64 = 1 << 7;

const PAT_UNCACHEABLE: u64 = 0x00;
const PAT_WRITE_COMBINING: u64 = 0x01;
const PAT_WRITE_THROUGH: u64 = 0x04;
const PAT_WRITE_PROTECTED: u64 = 0x05;
const PAT_WRITE_BACK: u64 = 0x06;
const PAT_UNCACHED: u64 = 0x07;

#[repr(u32)]
enum Idt {
    DE = 0,
    DB = 1,
    NMI = 2,
    BP = 3,
    OF = 4,
    BR = 5,
    UD = 6,
    NM = 7,
    DF = 8,
    FPUGP = 9,
    TS = 10,
    NP = 11,
    SS = 12,
    GP = 13,
    PF = 14,
    MF = 16,
    AC = 17,
    MC = 18,
    XF = 19,
}

const fn pat_value(i: u32, m: u64) -> u64 {
    (m << (8 * i)) as u64
}

const IA32_PAT_DEFAULT: u64 = pat_value(0, PAT_WRITE_BACK)
    | pat_value(1, PAT_WRITE_THROUGH)
    | pat_value(2, PAT_UNCACHED)
    | pat_value(3, PAT_UNCACHEABLE)
    | pat_value(4, PAT_WRITE_BACK)
    | pat_value(5, PAT_WRITE_THROUGH)
    | pat_value(6, PAT_UNCACHED)
    | pat_value(7, PAT_UNCACHEABLE);

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
    #[error("vcpu read vmcs")]
    VcpuReadVmcs,
    #[error("vcpu write vmcs")]
    VcpuWriteVmcs,
    #[error("vcpu set msr")]
    VcpuSetMsr,
    #[error("vcpu set vtimer mask")]
    VcpuSetVtimerMask,
    #[error("vcpu set apic address")]
    VcpuSetApicAddress,
    #[error("vcpu get apic state")]
    VcpuGetApicState,
    #[error("vcpu set apic state")]
    VcpuSetApicState,
    #[error("vcpu read apic")]
    VcpuReadApic,
    #[error("vcpu set apic")]
    VcpuSetApic,
    #[error("vm create")]
    VmCreate,
    #[error("space create")]
    SpaceCreate,
    #[error("init sregs")]
    InitSregs(arch::x86_64::regs::Error),
    #[error("vcpu exit apic access read")]
    VcpuExitApicAccessRead,
    #[error("page table walk")]
    VcpuPageWalk,
    #[error("read instruction")]
    VcpuReadInstruction,
    #[error("ioapic read")]
    VmIoapicRead,
    #[error("ioapic write")]
    VmIoapicWrite,
    #[error("ioapic assert irq")]
    VmIoapicAssertIrq,
    #[error("vcpu exit init ap")]
    VcpuExitInitAp,
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

    pub fn read_ioapic(&self, addr: hv_gpaddr_t) -> Result<u32, Error> {
        let mut value: u32 = 0;
        let ret = unsafe { hv_vm_ioapic_read(addr, &mut value) };
        if ret != HV_SUCCESS {
            Err(Error::VmIoapicRead)
        } else {
            Ok(value)
        }
    }

    pub fn write_ioapic(&self, addr: hv_gpaddr_t, value: u32) -> Result<(), Error> {
        let ret = unsafe { hv_vm_ioapic_write(addr, value) };
        if ret != HV_SUCCESS {
            Err(Error::VmIoapicWrite)
        } else {
            Ok(())
        }
    }

    pub fn assert_ioapic_irq(&self, irq: i32) -> Result<(), Error> {
        let ret = unsafe { hv_vm_ioapic_pulse_irq(irq) };
        if ret != HV_SUCCESS {
            Err(Error::VmIoapicAssertIrq)
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
    Canceled,
    #[cfg(target_arch = "aarch64")]
    CpuOn(u64, u64, u64),
    #[cfg(target_arch = "x86_64")]
    CpuOn {
        cpus: Vec<bool>,
        entry_rip: u64,
    },
    Handled,
    HypervisorCall,
    HypervisorIoCall {
        dev_id: usize,
        args_ptr: usize,
    },
    MmioRead(u64, &'a mut [u8]),
    MmioWrite(u64, &'a [u8]),
    Shutdown,
    IoPortRead(u16),
    IoPortWrite(u16, u64),
}

struct MmioRead {
    addr: u64,
    len: usize,
    dest_reg: u32,
}

pub struct HvfVcpu {
    parker: Arc<dyn Parkable>,
    vcpuid: hv_vcpuid_t,
    guest_mem: GuestMemoryMmap,
    hvf_vm: HvfVm,
    vcpu_count: usize,

    mmio_buf: [u8; 8],
    pending_mmio_read: Option<MmioRead>,
    pending_advance_rip: bool,

    cr0_mask0: u64,
    cr0_mask1: u64,
    cr4_mask0: u64,
    cr4_mask1: u64,
}

impl HvfVcpu {
    pub fn new(
        parker: Arc<dyn Parkable>,
        guest_mem: GuestMemoryMmap,
        hvf_vm: &HvfVm,
        vcpu_count: usize,
    ) -> Result<Self, Error> {
        let mut vcpuid: hv_vcpuid_t = 0;

        let ret =
            unsafe { hv_vcpu_create(&mut vcpuid, (HV_VCPU_DEFAULT | HV_VCPU_ACCEL_RDPMC) as u64) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuCreate);
        }

        // read cr0/cr4 fixed0/fixed1
        let cr0_fixed0 = Self::read_cap(hv_vmx_capability_t_HV_VMX_CAP_CR0_FIXED0)?;
        let cr4_fixed0 = Self::read_cap(hv_vmx_capability_t_HV_VMX_CAP_CR4_FIXED0)?;
        let cr0_fixed1 = Self::read_cap(hv_vmx_capability_t_HV_VMX_CAP_CR0_FIXED1)?;
        let cr4_fixed1 = Self::read_cap(hv_vmx_capability_t_HV_VMX_CAP_CR4_FIXED1)?;
        // convert to 1 and 0 masks
        let cr0_mask0 = !cr0_fixed0 & !cr0_fixed1;
        let mut cr0_mask1 = cr0_fixed0 & cr0_fixed1;
        let cr4_mask0 = !cr4_fixed0 & !cr4_fixed1;
        let cr4_mask1 = cr4_fixed0 & cr4_fixed1;

        // CR0_PE and CR0_PG are not fixed1 because we have unrestricted guest
        cr0_mask1 &= !(CR0_PG | CR0_PE);

        let vcpu = Self {
            parker,
            vcpuid,
            guest_mem,
            hvf_vm: hvf_vm.clone(),
            vcpu_count,

            mmio_buf: [0; 8],
            pending_mmio_read: None,
            pending_advance_rip: false,

            cr0_mask0,
            cr0_mask1,
            cr4_mask0,
            cr4_mask1,
        };
        vcpu.early_init()?;

        Ok(vcpu)
    }

    // this is NOT safe to run after ANY vCPU starts running,
    // because init_sregs manipulates guest memory to initialize page tables and descriptor tables
    fn early_init(&self) -> Result<(), Error> {
        // set APIC address
        let ret =
            unsafe { hv_vmx_vcpu_set_apic_address(self.vcpuid, APIC_DEFAULT_PHYS_BASE as u64) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuSetApicAddress);
        }

        // configure: lint0 = ExtINT, lint1 = NMI (as per MPTable)
        let lvt_lint0 = self.read_apic(APIC_LVT0)?;
        self.write_apic(
            APIC_LVT0,
            set_apic_delivery_mode(lvt_lint0, APIC_MODE_EXTINT),
        )?;
        let lvt_lint1 = self.read_apic(APIC_LVT1)?;
        self.write_apic(APIC_LVT1, set_apic_delivery_mode(lvt_lint1, APIC_MODE_NMI))?;

        debug!("set MSRs");
        for entry in arch::x86_64::msr::create_boot_msr_entries() {
            self.write_msr(entry.index, entry.data)?;
        }

        // HACK
        reg_init::just_initialize_hvf_already(self)?;

        // setup regs
        debug!("set regs");
        self.write_reg(hv_x86_reg_t_HV_X86_RFLAGS, 0x0000_0000_0000_0002u64)?;
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
            self.read_vmcs(VMCS_CTRL_VMENTRY_CONTROLS)? | IA32E_MODE_GUEST, // IA-32e mode guest
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

        // bhyve?
        self.write_vmcs(
            VMCS_CTRL_CR0_MASK,
            // we masked these out of cr0_mask1, but... add them back? otherwise we won't get vmexit for LME->LMA transition if EFER is written before CR0.PG=1
            // TODO: why does bhyve work without this?
            self.cr0_mask0 | self.cr0_mask1 | CR0_PG | CR0_PE,
        )?;
        self.write_vmcs(VMCS_CTRL_CR4_MASK, self.cr4_mask0 | self.cr4_mask1)?;
        // intel manual?
        self.write_vmcs(VMCS_CTRL_CR0_SHADOW, 0x60000010)?;
        self.write_vmcs(VMCS_CTRL_CR4_SHADOW, 0)?;

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

        self.write_reg(hv_x86_reg_t_HV_X86_DR6, 0xffff0ff0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_DR7, 0x400)?;

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

        self.enable_native_msr(HV_MSR_IA32_SPEC_CTRL)?;
        self.enable_native_msr(HV_MSR_IA32_PRED_CMD)?;
        self.enable_native_msr(HV_MSR_IA32_XSS)?;

        Ok(())
    }

    pub fn set_initial_state(&self, rip: u64, is_ap: bool) -> Result<(), Error> {
        if is_ap {
            // APs start in reset state, not Linux bootloader state
            self.reset_state()?;

            // AP boot rip can be >16 bits, so need to use CS and set RIP=0
            self.write_vmcs(VMCS_GUEST_CS_BASE, rip)?;
            self.write_reg(hv_x86_reg_t_HV_X86_CS, rip >> 4)?;
            self.write_reg(hv_x86_reg_t_HV_X86_RIP, 0)?;
        } else {
            self.write_reg(hv_x86_reg_t_HV_X86_RIP, rip)?;
        }

        Ok(())
    }

    pub fn id(&self) -> VcpuId {
        self.vcpuid as VcpuId
    }

    #[allow(non_upper_case_globals)]
    pub fn read_reg(&self, reg: u32) -> Result<u64, Error> {
        // CR0/CR3 have special handling in HVF, which messes up our state
        match reg {
            hv_x86_reg_t_HV_X86_CR0 => return self.read_vmcs(VMCS_GUEST_CR0),
            hv_x86_reg_t_HV_X86_CR3 => return self.read_vmcs(VMCS_GUEST_CR3),
            hv_x86_reg_t_HV_X86_CR4 => return self.read_vmcs(VMCS_GUEST_CR4),
            _ => {}
        }

        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_read_register(self.vcpuid, reg, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadRegister)
        } else {
            Ok(val)
        }
    }

    #[allow(non_upper_case_globals)]
    pub fn write_reg(&self, reg: u32, val: u64) -> Result<(), Error> {
        // CR0/CR3 have special handling in HVF, which messes up our states
        match reg {
            hv_x86_reg_t_HV_X86_CR0 => {
                self.write_vmcs(VMCS_GUEST_CR0, self.fix_cr0(val))?;
                self.write_vmcs(VMCS_CTRL_CR0_SHADOW, val)?;
                Ok(())
            }
            hv_x86_reg_t_HV_X86_CR3 => self.write_vmcs(VMCS_GUEST_CR3, val),
            hv_x86_reg_t_HV_X86_CR4 => {
                self.write_vmcs(VMCS_GUEST_CR4, self.fix_cr4(val))?;
                self.write_vmcs(VMCS_CTRL_CR4_SHADOW, val)?;
                Ok(())
            }
            _ => {
                let ret = unsafe { hv_vcpu_write_register(self.vcpuid, reg, val) };
                if ret != HV_SUCCESS {
                    Err(Error::VcpuSetRegister)
                } else {
                    Ok(())
                }
            }
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
            Err(Error::VcpuReadVmcs)
        } else {
            Ok(val)
        }
    }

    fn read_cap(field: hv_vmx_capability_t) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vmx_read_capability(field, &mut val) };

        if ret != HV_SUCCESS {
            Err(Error::VcpuReadCapability)
        } else {
            Ok(val)
        }
    }

    fn get_msr_info(field: u32) -> Result<u64, Error> {
        let mut value: u64 = 0;
        let ret = unsafe { hv_vmx_get_msr_info(field as hv_vmx_msr_info_t, &mut value) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadMsr)
        } else {
            Ok(value)
        }
    }

    fn write_vmcs(&self, field: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vmx_vcpu_write_vmcs(self.vcpuid, field, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuWriteVmcs)
        } else {
            Ok(())
        }
    }

    fn read_apic(&self, offset: u32) -> Result<u32, Error> {
        let mut val: u32 = 0;
        let ret = unsafe { hv_vcpu_apic_read(self.vcpuid, offset, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadApic)
        } else {
            Ok(val)
        }
    }

    fn write_apic(&self, offset: u32, val: u32) -> Result<bool, Error> {
        let mut no_side_effect: bool = false;
        let ret = unsafe { hv_vcpu_apic_write(self.vcpuid, offset, val, &mut no_side_effect) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetApic)
        } else {
            Ok(no_side_effect)
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

    #[allow(non_upper_case_globals)]
    pub fn run(&mut self) -> Result<VcpuExit, Error> {
        self.parker.before_vcpu_run(self.vcpuid as VcpuId);

        if self.parker.should_shutdown() {
            return Ok(VcpuExit::Shutdown);
        }

        if let Some(mmio_read) = self.pending_mmio_read.take() {
            let val = match mmio_read.len {
                1 => u8::from_le_bytes(self.mmio_buf[0..1].try_into().unwrap()) as u64,
                2 => u16::from_le_bytes(self.mmio_buf[0..2].try_into().unwrap()) as u64,
                4 => u32::from_le_bytes(self.mmio_buf[0..4].try_into().unwrap()) as u64,
                8 => u64::from_le_bytes(self.mmio_buf[0..8].try_into().unwrap()),
                _ => panic!(
                    "unsupported mmio pa={} len={}",
                    mmio_read.addr, mmio_read.len
                ),
            };

            self.write_reg(mmio_read.dest_reg, val)?;
        }

        // must be here for lifetime reasons (MmioWrite borrows self as mutable)
        if self.pending_advance_rip {
            let rip = self.read_reg(hv_x86_reg_t_HV_X86_RIP)?;
            let instr_len = self.read_vmcs(VMCS_RO_VMEXIT_INSTR_LEN)?;
            self.write_reg(hv_x86_reg_t_HV_X86_RIP, rip + instr_len)?;
        }

        let ret = unsafe { hv_vcpu_run_until(self.vcpuid, HV_DEADLINE_FOREVER) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuRun);
        }

        let mut exit_info: hv_vm_exitinfo_t = 0;
        let ret = unsafe { hv_vcpu_exit_info(self.vcpuid, &mut exit_info) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuRun);
        }

        self.pending_advance_rip = true;
        let res = match exit_info {
            hv_vm_exitinfo_t_HV_VM_EXITINFO_VMX => {
                let exit_reason = self.read_vmcs(VMCS_RO_EXIT_REASON)? as u32;
                match exit_reason {
                    VMX_REASON_VMCALL => Ok(VcpuExit::HypervisorCall),

                    VMX_REASON_CPUID => {
                        // TODO: filter + enumerate host
                        let eax = self.read_reg(hv_x86_reg_t_HV_X86_RAX)? & 0xffffffff;
                        let ecx = self.read_reg(hv_x86_reg_t_HV_X86_RCX)? & 0xffffffff;
                        let mut res = unsafe { __cpuid_count(eax as u32, ecx as u32) };

                        match (eax as u32, ecx as u32) {
                            (1, _) => {
                                // we use HLT, which handled by HVC APIC
                                res.ecx &= !(1 << 3); // monitor/mwait

                                res.ecx |= 1 << 31; // HYPERVISOR
                                res.edx &= !(1 << 29); // Automatic Clock Control
                            }
                            (6, _) => {
                                res.eax &= !(1 << 0); // Digital Thermal Sensor
                                res.ecx &= !(1 << 0); // APERFMPERF
                            }
                            (7, 0) => {
                                res.ebx &= !(1 << 1); // TODO: TSC_ADJUST (msr 0x3b)?
                                res.ebx &= !(1 << 25); // Intel Processor Trace

                                // macOS silently fails to set XCR0 (i.e. write_reg(XCR0) has no effect) if Linux attempts to enable MPX (XCR0[3:4]), which breaks AVX
                                res.ebx &= !(1 << 14); // MPX
                            }
                            (10, _) => {
                                res.eax = 0; // TODO: PMU
                            }
                            (CPUID_XSTATE, 0) => {
                                // ebx = size for XSAVE, with features in XCR0+XSS
                                // since host XCR0 and XSS are different, we need to recalculate this, instead of relying on whatever CPUID returns with macOS' XCR0 and XSS
                                res.ebx = self.calculate_xsave_size(false)?;
                            }
                            (CPUID_XSTATE, 1) => {
                                // ebx = size for XSAVEC/XSAVES, with features in XCR0+XSS
                                // since host XCR0 and XSS are different, we need to recalculate this, instead of relying on whatever CPUID returns with macOS' XCR0 and XSS
                                // we support XSAVEC/XSAVES on supported CPUs, so this case is possible
                                res.ebx = self.calculate_xsave_size(true)?;
                            }
                            (CPUID_KVM, _) => {
                                // "KVMKVMKVM\0\0\0"
                                // we report no features, but this is good: it allows x2apic and disables hardlockup detector
                                res.eax = CPUID_KVM_FEATURES; // no KVM_CPUID_FEATURES
                                res.ebx = 0x4b564d4bu32.swap_bytes();
                                res.ecx = 0x564d4b56u32.swap_bytes();
                                res.edx = 0x4d000000u32.swap_bytes();
                            }
                            (CPUID_KVM_FEATURES, _) => {
                                res.eax = 0;
                                res.ebx = 0;
                                res.ecx = 0;
                                res.edx = 0;
                            }
                            _ => {}
                        }

                        self.write_reg(hv_x86_reg_t_HV_X86_RAX, res.eax as u64)?;
                        self.write_reg(hv_x86_reg_t_HV_X86_RCX, res.ecx as u64)?;
                        self.write_reg(hv_x86_reg_t_HV_X86_RDX, res.edx as u64)?;
                        self.write_reg(hv_x86_reg_t_HV_X86_RBX, res.ebx as u64)?;
                        Ok(VcpuExit::Handled)
                    }

                    VMX_REASON_MOV_CR => {
                        let qual = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                        match qual & 0xf {
                            0 => {
                                // cr0
                                // must be mov to cr0
                                if qual & 0xf0 != 0 {
                                    panic!("unexpected mov cr0 exit (not reg): {qual}");
                                }

                                let reg = (qual >> 8) & 0xf;
                                let value = self.read_reg(map_indexed_reg(reg as u8))? as u64;
                                self.write_reg(hv_x86_reg_t_HV_X86_CR0, value)?;

                                if value & CR0_PG != 0 {
                                    let mut efer = self.read_vmcs(VMCS_GUEST_IA32_EFER)?;
                                    if efer & EFER_LME as u64 != 0 {
                                        // why do we set EFER_LMA here? FreeBSD does it
                                        efer |= EFER_LMA as u64;
                                        self.write_vmcs(VMCS_GUEST_IA32_EFER, efer)?;

                                        self.write_vmcs(
                                            VMCS_CTRL_VMENTRY_CONTROLS,
                                            self.read_vmcs(VMCS_CTRL_VMENTRY_CONTROLS)?
                                                | IA32E_MODE_GUEST,
                                        )?;
                                    }
                                }
                            }

                            4 => {
                                // cr4
                                // must be mov to cr4
                                if qual & 0xf0 != 0 {
                                    panic!("unexpected mov cr4 exit (not reg): {qual}");
                                }

                                let reg = (qual >> 8) & 0xf;
                                let value = self.read_reg(map_indexed_reg(reg as u8))? as u64;
                                self.write_reg(hv_x86_reg_t_HV_X86_CR4, value)?;
                            }

                            8 => {
                                // cr8
                                // must be to/from a register
                                if qual & 0xe0 != 0 {
                                    panic!("unexpected mov cr8 exit (not reg): {qual}");
                                }

                                // read or write?
                                let reg = (qual >> 8) & 0xf;
                                if qual & 0x10 != 0 {
                                    // read
                                    let tpr = self.read_apic(LAPIC_TPR)? as u64;
                                    self.write_reg(map_indexed_reg(reg as u8), tpr >> 4)?;
                                } else {
                                    // write
                                    let tpr = self.read_reg(map_indexed_reg(reg as u8))? as u32;
                                    self.write_apic(LAPIC_TPR, tpr << 4)?;
                                }
                            }

                            _ => panic!("unexpected mov cr exit: {qual}"),
                        }

                        Ok(VcpuExit::Handled)
                    }

                    VMX_REASON_RDMSR => {
                        let msr_id = self.read_reg(hv_x86_reg_t_HV_X86_RCX)? & 0xffffffff;
                        let value = match msr_id as u32 {
                            MSR_EFER => self.read_vmcs(VMCS_GUEST_IA32_EFER)?,
                            MSR_IA32_MISC_ENABLE => IA32_MISC_ENABLE_VALUE,
                            MSR_IA32_UCODE_REV => 0,
                            HV_MSR_IA32_ARCH_CAPABILITIES => {
                                Self::get_msr_info(HV_VMX_INFO_MSR_IA32_ARCH_CAPABILITIES)?
                            }
                            MSR_IA32_PERF_CAPABILITIES => {
                                Self::get_msr_info(HV_VMX_INFO_MSR_IA32_PERF_CAPABILITIES)?
                            }
                            MSR_IA32_FEATURE_CONTROL => FEAT_CTL_LOCKED,
                            MSR_MISC_FEATURE_ENABLES => 0,
                            MSR_PLATFORM_INFO => 0,
                            MSR_TSX_FORCE_ABORT => 0,
                            MSR_IA32_TSX_CTRL => 0,
                            MSR_RAPL_POWER_UNIT => 0,
                            MSR_PP0_ENERGY_STATUS => 0,
                            MSR_PP1_ENERGY_STATUS => 0,
                            MSR_PKG_ENERGY_STATUS => 0,
                            MSR_DRAM_ENERGY_STATUS => 0,
                            MSR_PLATFORM_ENERGY_STATUS => 0,
                            MSR_PPERF => 0,
                            MSR_SMI_COUNT => 0,
                            MSR_MTRRcap => 0,
                            MSR_MTRRdefType => 0,
                            MSR_MTRRfix4K_C0000 => 0,
                            MSR_MTRRfix4K_C8000 => 0,
                            MSR_MTRRfix4K_D0000 => 0,
                            MSR_MTRRfix4K_D8000 => 0,
                            MSR_MTRRfix4K_E0000 => 0,
                            MSR_MTRRfix4K_E8000 => 0,
                            MSR_MTRRfix4K_F0000 => 0,
                            MSR_MTRRfix4K_F8000 => 0,
                            MSR_MTRRfix16K_80000 => 0,
                            MSR_MTRRfix16K_A0000 => 0,
                            MSR_MTRRfix64K_00000 => 0,
                            MSR_IA32_CR_PAT => IA32_PAT_DEFAULT,
                            _ => panic!("unexpected rdmsr exit: {msr_id:x}"),
                        };

                        self.write_reg(hv_x86_reg_t_HV_X86_RAX, value)?;
                        self.write_reg(hv_x86_reg_t_HV_X86_RDX, value >> 32)?;
                        Ok(VcpuExit::Handled)
                    }

                    VMX_REASON_WRMSR => {
                        let msr_id = self.read_reg(hv_x86_reg_t_HV_X86_RCX)? & 0xffffffff;
                        let edx = self.read_reg(hv_x86_reg_t_HV_X86_RDX)? & 0xffffffff;
                        let eax = self.read_reg(hv_x86_reg_t_HV_X86_RAX)? & 0xffffffff;
                        let value = (edx as u64) << 32 | (eax as u64);
                        match msr_id as u32 {
                            MSR_EFER => {
                                self.write_vmcs(VMCS_GUEST_IA32_EFER, value)?;

                                // IA-32e mode guest == EFER.LMA
                                let mut vmentry_ctl = self.read_vmcs(VMCS_CTRL_VMENTRY_CONTROLS)?;
                                if value & EFER_LMA as u64 != 0 {
                                    vmentry_ctl |= IA32E_MODE_GUEST;
                                } else {
                                    vmentry_ctl &= !IA32E_MODE_GUEST;
                                }
                                self.write_vmcs(VMCS_CTRL_VMENTRY_CONTROLS, vmentry_ctl)?;
                            }
                            MSR_IA32_UCODE_REV => {}
                            MSR_MISC_FEATURE_ENABLES => {}
                            MSR_TSX_FORCE_ABORT => {}
                            MSR_IA32_TSX_CTRL => {}
                            // this is to enable x2APIC, but macOS doesn't have a way for us to propagate it
                            MSR_IA32_APICBASE => {}
                            MSR_MTRRcap => {}
                            MSR_MTRRdefType => {}
                            MSR_MTRRfix4K_C0000 => {}
                            MSR_MTRRfix4K_C8000 => {}
                            MSR_MTRRfix4K_D0000 => {}
                            MSR_MTRRfix4K_D8000 => {}
                            MSR_MTRRfix4K_E0000 => {}
                            MSR_MTRRfix4K_E8000 => {}
                            MSR_MTRRfix4K_F0000 => {}
                            MSR_MTRRfix4K_F8000 => {}
                            MSR_MTRRfix16K_80000 => {}
                            MSR_MTRRfix16K_A0000 => {}
                            MSR_MTRRfix64K_00000 => {}
                            // macOS doesn't let us set VMCS_GUEST_IA32_PAT
                            MSR_IA32_CR_PAT => {}
                            _ => panic!("unexpected wrmsr exit: {msr_id:x}"),
                        }
                        Ok(VcpuExit::Handled)
                    }

                    // HVF APIC handles HLT
                    VMX_REASON_HLT => panic!("unexpected hlt"),
                    // we hide monitor/mwait, but just just in case userspace uses it...
                    VMX_REASON_MONITOR => Ok(VcpuExit::Handled),
                    VMX_REASON_MWAIT => Ok(VcpuExit::Handled),

                    VMX_REASON_IO => {
                        let qual = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                        let input = (qual & 8) != 0;
                        let string = (qual & 0x10) != 0;
                        let port: u16 = ((qual >> 16) & 0xffff) as u16;
                        if string {
                            panic!("string port io unsupported");
                        }

                        if input {
                            // not supported, except for earlycon LSR
                            let value = if port == 0x3f8 + 5 {
                                // LSR_EMPTY_BIT | LSR_IDLE_BIT (THR is empty, line is idle)
                                0x20 | 0x40
                            } else {
                                0
                            };

                            self.write_reg(hv_x86_reg_t_HV_X86_RAX, value)?;
                            Ok(VcpuExit::IoPortRead(port))
                        } else {
                            let value = self.read_reg(hv_x86_reg_t_HV_X86_RAX)?;
                            Ok(VcpuExit::IoPortWrite(port, value))
                        }
                    }

                    VMX_REASON_IRQ => {
                        // external interrupt on host - nothing to do
                        self.pending_advance_rip = false;
                        Ok(VcpuExit::Handled)
                    }

                    VMX_REASON_TRIPLE_FAULT => {
                        let gpa = self.read_vmcs(VMCS_GUEST_PHYSICAL_ADDRESS)?;
                        let gla = self.read_vmcs(VMCS_RO_GUEST_LIN_ADDR)?;
                        let rip = self.read_reg(hv_x86_reg_t_HV_X86_RIP)?;
                        let insn = self.decode_current_insn()?;
                        self.dump_vmcs();
                        panic!(
                            "triple fault: gpa={:#018x} gla={:#018x} rip={:#018x} insn={:?}",
                            gpa, gla, rip, insn
                        );
                    }

                    VMX_REASON_EPT_VIOLATION => {
                        let qual = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                        let is_write = (qual & EPT_VIOLATION_DATA_WRITE) != 0;
                        if qual & EPT_VIOLATION_INST_FETCH != 0 {
                            panic!("instruction fetch EPT violation");
                        }

                        let insn = self.decode_current_insn()?;
                        let gpa = self.read_vmcs(VMCS_GUEST_PHYSICAL_ADDRESS)?;
                        debug!(
                            "EPT violation: gpa={:x} insn={:?} write={}",
                            gpa, insn, is_write
                        );

                        match gpa {
                            // ioapic is handled here because it's emulated by HVF
                            IOAPIC_START..=IOAPIC_END_INCL => {
                                if is_write {
                                    if insn.code() != Code::Mov_rm32_r32 {
                                        panic!(
                                            "unexpected write instruction on IOAPIC: {:?}",
                                            insn
                                        );
                                    }

                                    let reg = map_insn_reg(insn.op1_register());
                                    let value = self.read_reg(reg)?;
                                    self.hvf_vm.write_ioapic(gpa, value as u32)?;
                                } else {
                                    if insn.code() != Code::Mov_r32_rm32 {
                                        panic!("unexpected read instruction on IOAPIC: {:?}", insn);
                                    }

                                    let reg = map_insn_reg(insn.op0_register());
                                    let value = self.hvf_vm.read_ioapic(gpa)?;
                                    self.write_reg(reg, value as u64)?;
                                }

                                Ok(VcpuExit::Handled)
                            }

                            // everything else goes to MMIO bus
                            _ => {
                                if is_write {
                                    let len = match insn.code() {
                                        Code::Mov_rm8_r8 => 1,
                                        Code::Mov_rm16_r16 => 2,
                                        Code::Mov_rm32_r32 => 4,
                                        Code::Mov_rm64_r64 => 8,
                                        _ => panic!(
                                            "unexpected write instruction on MMIO: {:?}",
                                            insn
                                        ),
                                    };

                                    let reg = map_insn_reg(insn.op1_register());
                                    let value = self.read_reg(reg)?;
                                    match len {
                                        1 => self.mmio_buf[0..1]
                                            .copy_from_slice(&(value as u8).to_le_bytes()),
                                        2 => self.mmio_buf[0..2]
                                            .copy_from_slice(&(value as u16).to_le_bytes()),
                                        4 => self.mmio_buf[0..4]
                                            .copy_from_slice(&(value as u32).to_le_bytes()),
                                        8 => self.mmio_buf[0..8]
                                            .copy_from_slice(&(value).to_le_bytes()),
                                        _ => panic!("unexpected MMIO write size"),
                                    }

                                    Ok(VcpuExit::MmioWrite(gpa, &self.mmio_buf[0..len]))
                                } else {
                                    let len = match insn.code() {
                                        Code::Mov_r8_rm8 => 1,
                                        Code::Mov_r16_rm16 => 2,
                                        Code::Mov_r32_rm32 => 4,
                                        Code::Mov_r64_rm64 => 8,
                                        _ => panic!(
                                            "unexpected read instruction on MMIO: {:?}",
                                            insn
                                        ),
                                    };

                                    let reg = map_insn_reg(insn.op0_register());
                                    self.pending_mmio_read = Some(MmioRead {
                                        addr: gpa,
                                        len,
                                        dest_reg: reg,
                                    });
                                    Ok(VcpuExit::MmioRead(gpa, &mut self.mmio_buf[0..len]))
                                }
                            }
                        }
                    }

                    VMX_REASON_APIC_ACCESS => {
                        let qual = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                        let offset = qual & 0xfff;
                        let typ = (qual >> 12) & 0xf;
                        if typ != 1 {
                            // only writes should hit this path
                            panic!("unexpected APIC access: qual={:x}", qual);
                        }

                        let insn = self.decode_current_insn()?;
                        if insn.code() != Code::Mov_rm32_r32 {
                            panic!("unexpected APIC access instruction: {:?}", insn);
                        }

                        let reg = map_insn_reg(insn.op1_register());
                        let value = self.read_reg(reg)?;
                        self.write_apic(offset as u32, value as u32)?;
                        Ok(VcpuExit::Handled)
                    }

                    VMX_REASON_XSETBV => {
                        // only xcr0 is defined
                        let xcr = self.read_reg(hv_x86_reg_t_HV_X86_RCX)?;
                        if xcr != 0 {
                            panic!("unexpected XSETBV: xcr={:x}", xcr);
                        }

                        let edx = self.read_reg(hv_x86_reg_t_HV_X86_RDX)? & 0xffffffff;
                        let eax = self.read_reg(hv_x86_reg_t_HV_X86_RAX)? & 0xffffffff;
                        let value = (edx as u64) << 32 | (eax as u64);
                        self.write_reg(hv_x86_reg_t_HV_X86_XCR0, value)?;
                        Ok(VcpuExit::Handled)
                    }

                    _ => {
                        self.dump_vmcs();
                        panic!(
                            "unexpected exit reason: vcpuid={} {} - qual={:x}",
                            self.vcpuid,
                            exit_reason,
                            self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?
                        )
                    }
                }
            }

            // we get the same is_actv array below, so no need to handle INIT_AP
            hv_vm_exitinfo_t_HV_VM_EXITINFO_INIT_AP => Ok(VcpuExit::Handled),
            hv_vm_exitinfo_t_HV_VM_EXITINFO_STARTUP_AP => {
                let mut is_actv = vec![false; self.vcpu_count];
                let mut ap_rip: u64 = 0;
                let ret = unsafe {
                    hv_vcpu_exit_startup_ap(
                        self.vcpuid,
                        is_actv.as_mut_ptr(),
                        is_actv.len() as u32,
                        &mut ap_rip,
                    )
                };
                if ret != HV_SUCCESS {
                    return Err(Error::VcpuExitInitAp);
                }

                Ok(VcpuExit::CpuOn {
                    cpus: is_actv,
                    entry_rip: ap_rip,
                })
            }

            // should never get this
            hv_vm_exitinfo_t_HV_VM_EXITINFO_IOAPIC_EOI => panic!("unexpected IOAPIC EOI"),
            hv_vm_exitinfo_t_HV_VM_EXITINFO_INJECT_EXCP => todo!("APIC inject exception"),
            // TODO: need to test this
            hv_vm_exitinfo_t_HV_VM_EXITINFO_SMI => todo!("SMI"),

            // only for reads. we still get VMX_REASON_APIC_ACCESS for writes
            hv_vm_exitinfo_t_HV_VM_EXITINFO_APIC_ACCESS_READ => {
                let qual = self.read_vmcs(VMCS_RO_EXIT_QUALIFIC)?;
                let offset = qual & 0xfff;
                let typ = (qual >> 12) & 0xf;

                let insn = self.decode_current_insn()?;
                if insn.code() != Code::Mov_r32_rm32 {
                    panic!("unexpected instruction in apic access read: {:?}", insn);
                }

                let mut value: u32 = 0;
                let ret = unsafe { hv_vcpu_exit_apic_access_read(self.vcpuid, &mut value) };
                if ret != HV_SUCCESS {
                    return Err(Error::VcpuExitApicAccessRead);
                }

                debug!(
                    "apic access read: qual={:x} offset={:x} typ={:x} insn={:?} => {:x}",
                    qual, offset, typ, insn, value
                );

                let reg = map_insn_reg(insn.op0_register());
                self.write_reg(reg, value as u64)?;
                Ok(VcpuExit::Handled)
            }

            _ => panic!("unknown exit info: {:x}", exit_info),
        }?;

        Ok(res)
    }

    pub fn destroy(self) {
        let err = unsafe { hv_vcpu_destroy(self.vcpuid) };
        if err != 0 {
            error!("Failed to destroy vcpu: {err}");
        }
    }

    fn fix_cr0(&self, cr0: u64) -> u64 {
        (cr0 | self.cr0_mask1) & !self.cr0_mask0
    }

    fn fix_cr4(&self, cr4: u64) -> u64 {
        (cr4 | self.cr4_mask1) & !self.cr4_mask0
    }

    fn resolve_virtual_address(&self, gva: GuestAddress) -> Result<GuestAddress, Error> {
        // is paging enabled?
        let cr0 = self.read_reg(hv_x86_reg_t_HV_X86_CR0)?;
        if cr0 & CR0_PG == 0 {
            panic!("paging not enabled");
        }

        // walk page tables
        let pml4_index = (gva.raw_value() >> 39) & 0x1ff;
        let pml4_addr = self.read_reg(hv_x86_reg_t_HV_X86_CR3)? & 0xfffffffffffff000;
        let pml4: u64 = self
            .guest_mem
            .read_obj(GuestAddress(pml4_addr + (pml4_index * 8)))
            .map_err(|_| Error::VcpuPageWalk)?;
        if pml4 & PTE_PRESENT == 0 {
            return Err(Error::VcpuPageWalk);
        }
        // PML4 has no PAGE_SIZE bit

        let pdp_index = (gva.raw_value() >> 30) & 0x1ff;
        let pdp_addr = pml4 & (0xffffffffff << 12);
        let pdp: u64 = self
            .guest_mem
            .read_obj(GuestAddress(pdp_addr + (pdp_index * 8)))
            .map_err(|_| Error::VcpuPageWalk)?;
        if pdp & PTE_PRESENT == 0 {
            return Err(Error::VcpuPageWalk);
        }
        if pdp & PTE_PAGE_SIZE != 0 {
            // terminate walk with 1 GiB page (30 bits)
            let page_offset = gva.raw_value() & 0x3fffffff;
            let page_addr = pdp & (0xffffffffff << 12);
            return Ok(GuestAddress(page_addr | page_offset));
        }

        let pd_index = (gva.raw_value() >> 21) & 0x1ff;
        let pd_addr = pdp & (0xffffffffff << 12);
        let pd: u64 = self
            .guest_mem
            .read_obj(GuestAddress(pd_addr + (pd_index * 8)))
            .map_err(|_| Error::VcpuPageWalk)?;
        if pd & PTE_PRESENT == 0 {
            return Err(Error::VcpuPageWalk);
        }
        if pd & PTE_PAGE_SIZE != 0 {
            // terminate walk with 2 MiB page (21 bits)
            let page_offset = gva.raw_value() & 0x1fffff;
            let page_addr = pd & (0xffffffffff << 12);
            return Ok(GuestAddress(page_addr | page_offset));
        }

        let pt_index = (gva.raw_value() >> 12) & 0x1ff;
        let pt_addr = pd & (0xffffffffff << 12);
        let pt: u64 = self
            .guest_mem
            .read_obj(GuestAddress(pt_addr + (pt_index * 8)))
            .map_err(|_| Error::VcpuPageWalk)?;
        if pt & PTE_PRESENT == 0 {
            return Err(Error::VcpuPageWalk);
        }
        // PAGE_SIZE doesn't make sense for PT

        let page_offset = gva.raw_value() & 0xfff;
        let page_addr = pt & (0xffffffffff << 12);
        Ok(GuestAddress(page_addr | page_offset))
    }

    fn decode_current_insn(&self) -> Result<Instruction, Error> {
        let rip = self.read_reg(hv_x86_reg_t_HV_X86_RIP)?;
        let rip_phys = self.resolve_virtual_address(GuestAddress(rip as u64))?;

        let instr_len = self.read_vmcs(VMCS_RO_VMEXIT_INSTR_LEN)?;
        let mut instr_buf = [0u8; 16];
        self.guest_mem
            .read_slice(&mut instr_buf[..instr_len as usize], rip_phys)
            .map_err(|_| Error::VcpuReadInstruction)?;

        let mut decoder = Decoder::with_ip(
            64,
            &instr_buf[..instr_len as usize],
            0,
            DecoderOptions::NONE,
        );
        let instr = decoder.decode();

        Ok(instr)
    }

    // TODO: causes entry failure
    fn inject_exception(&self, vector: Idt, error_code: Option<u32>) -> Result<(), Error> {
        self.clear_guest_intr_shadow()?;

        let info = (vector as u64) & 0xff | VM_INTINFO_VALID | VM_INTINFO_HWEXCEPTION;
        if let Some(error_code) = error_code {
            self.write_vmcs(VMCS_CTRL_VMENTRY_EXC_ERROR, error_code as u64)?;
        }
        self.write_vmcs(VMCS_CTRL_VMENTRY_IRQ_INFO, info)?;
        Ok(())
    }

    fn clear_guest_intr_shadow(&self) -> Result<(), Error> {
        let mut intr = self.read_vmcs(VMCS_GUEST_INTERRUPTIBILITY)?;
        intr &= !(GUEST_INTRBILITY_MOVSS_BLOCKING as u64 | GUEST_INTRBILITY_STI_BLOCKING as u64);
        self.write_vmcs(VMCS_GUEST_INTERRUPTIBILITY, intr)?;
        Ok(())
    }

    fn calculate_xsave_size(&self, compact: bool) -> Result<u32, Error> {
        let mut features = self.read_reg(hv_x86_reg_t_HV_X86_XCR0)?;
        if compact {
            // compact = XSAVES, i.e. including supervisor state
            features |= self.read_msr(MSR_IA32_XSS)?;

            // in compact mode, calculate size by adding the header size, plus the size of each feature
            // header size is based on the 2 non-extended/legacy features: FP and SSE, but their sizes are *different* in compact mode!
            let mut size = 512 + 64;
            let highest_feature_index = features.ilog2() as u32;
            // first extended feature index = 2
            for feature_index in 2..=highest_feature_index as u32 {
                if features & (1 << feature_index) == 0 {
                    continue;
                }

                let info = get_xstate_info(feature_index);
                // needs alignment?
                if info.flags & XSTATE_FLAG_ALIGNED != 0 {
                    size = (size + 63) / 64 * 64;
                }
                size += info.size;
            }
            Ok(size)
        } else {
            // in standard mode, calculate size by taking the offset + size of the highest feature
            let highest_feature_index = features.ilog2() as u32;
            let info = get_xstate_info(highest_feature_index);
            let offset = info.offset.unwrap();
            Ok(offset + info.size)
        }
    }

    /*
     * From Intel Vol 3a:
     * Table 9-1. IA-32 Processor States Following Power-up, Reset or INIT
     */
    fn reset_state(&self) -> Result<(), Error> {
        self.write_reg(hv_x86_reg_t_HV_X86_RFLAGS, 0x2)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RIP, 0xfff0)?;

        /*
         * According to Intels Software Developer Manual CR0 should be
         * initialized with CR0_ET | CR0_NW | CR0_CD but that crashes some
         * guests like Windows.
         */
        self.write_reg(hv_x86_reg_t_HV_X86_CR0, CR0_NE)?;
        self.write_reg(hv_x86_reg_t_HV_X86_CR2, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_CR3, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_CR4, 0)?;

        /*
         * CS: present, r/w, accessed, 16-bit, byte granularity, usable
         */
        self.write_vmcs(VMCS_GUEST_CS_BASE, 0xffff0000)?;
        self.write_vmcs(VMCS_GUEST_CS_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_CS_AR, 0x0093)?;
        self.write_reg(hv_x86_reg_t_HV_X86_CS, 0xf000)?;

        /*
         * SS,DS,ES,FS,GS: present, r/w, accessed, 16-bit, byte granularity
         */
        self.write_vmcs(VMCS_GUEST_SS_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_SS_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_SS_AR, 0x0093)?;
        self.write_reg(hv_x86_reg_t_HV_X86_SS, 0)?;
        self.write_vmcs(VMCS_GUEST_DS_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_DS_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_DS_AR, 0x0093)?;
        self.write_reg(hv_x86_reg_t_HV_X86_DS, 0)?;
        self.write_vmcs(VMCS_GUEST_ES_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_ES_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_ES_AR, 0x0093)?;
        self.write_reg(hv_x86_reg_t_HV_X86_ES, 0)?;
        self.write_vmcs(VMCS_GUEST_FS_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_FS_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_FS_AR, 0x0093)?;
        self.write_reg(hv_x86_reg_t_HV_X86_FS, 0)?;
        self.write_vmcs(VMCS_GUEST_GS_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_GS_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_GS_AR, 0x0093)?;
        self.write_reg(hv_x86_reg_t_HV_X86_GS, 0)?;

        self.write_vmcs(VMCS_GUEST_IA32_EFER, 0)?;

        /* General purpose registers */
        self.write_reg(hv_x86_reg_t_HV_X86_RAX, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RBX, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RCX, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RDX, 0xf00)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RSI, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RDI, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RBP, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_RSP, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R8, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R9, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R10, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R11, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R12, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R13, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R14, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_R15, 0)?;

        /* GDTR, IDTR */
        self.write_reg(hv_x86_reg_t_HV_X86_GDT_BASE, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_GDT_LIMIT, 0xffff)?;
        self.write_reg(hv_x86_reg_t_HV_X86_IDT_BASE, 0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_IDT_LIMIT, 0xffff)?;

        /* TR */
        self.write_vmcs(VMCS_GUEST_TR_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_TR_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_TR_AR, 0x0000008b)?;
        self.write_reg(hv_x86_reg_t_HV_X86_TR, 0)?;

        /* LDTR */
        self.write_vmcs(VMCS_GUEST_LDTR_BASE, 0)?;
        self.write_vmcs(VMCS_GUEST_LDTR_LIMIT, 0xffff)?;
        self.write_vmcs(VMCS_GUEST_LDTR_AR, 0x00000082)?;
        self.write_reg(hv_x86_reg_t_HV_X86_LDTR, 0)?;

        self.write_reg(hv_x86_reg_t_HV_X86_DR6, 0xffff0ff0)?;
        self.write_reg(hv_x86_reg_t_HV_X86_DR7, 0x400)?;

        self.clear_guest_intr_shadow()?;

        // clear IA32E_MODE_GUEST to pass validation when EFER=0
        self.write_vmcs(
            VMCS_CTRL_VMENTRY_CONTROLS,
            self.read_vmcs(VMCS_CTRL_VMENTRY_CONTROLS)? & !IA32E_MODE_GUEST,
        )?;

        Ok(())
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
            ("VMCS_RO_INSTR_ERROR", VMCS_RO_INSTR_ERROR),
            ("VMCS_RO_EXIT_REASON", VMCS_RO_EXIT_REASON),
            ("VMCS_RO_VMEXIT_IRQ_INFO", VMCS_RO_VMEXIT_IRQ_INFO),
            ("VMCS_RO_VMEXIT_IRQ_ERROR", VMCS_RO_VMEXIT_IRQ_ERROR),
            ("VMCS_RO_IDT_VECTOR_INFO", VMCS_RO_IDT_VECTOR_INFO),
            ("VMCS_RO_IDT_VECTOR_ERROR", VMCS_RO_IDT_VECTOR_ERROR),
            ("VMCS_RO_VMEXIT_INSTR_LEN", VMCS_RO_VMEXIT_INSTR_LEN),
            ("VMCS_RO_VMX_INSTR_INFO", VMCS_RO_VMX_INSTR_INFO),
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
            ("VMCS_RO_EXIT_QUALIFIC", VMCS_RO_EXIT_QUALIFIC),
            ("VMCS_RO_IO_RCX", VMCS_RO_IO_RCX),
            ("VMCS_RO_IO_RSI", VMCS_RO_IO_RSI),
            ("VMCS_RO_IO_RDI", VMCS_RO_IO_RDI),
            ("VMCS_RO_IO_RIP", VMCS_RO_IO_RIP),
            ("VMCS_RO_GUEST_LIN_ADDR", VMCS_RO_GUEST_LIN_ADDR),
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

fn map_indexed_reg(reg: u8) -> u32 {
    match reg {
        0 => hv_x86_reg_t_HV_X86_RAX,
        1 => hv_x86_reg_t_HV_X86_RCX,
        2 => hv_x86_reg_t_HV_X86_RDX,
        3 => hv_x86_reg_t_HV_X86_RBX,
        4 => hv_x86_reg_t_HV_X86_RSP,
        5 => hv_x86_reg_t_HV_X86_RBP,
        6 => hv_x86_reg_t_HV_X86_RSI,
        7 => hv_x86_reg_t_HV_X86_RDI,
        8 => hv_x86_reg_t_HV_X86_R8,
        9 => hv_x86_reg_t_HV_X86_R9,
        10 => hv_x86_reg_t_HV_X86_R10,
        11 => hv_x86_reg_t_HV_X86_R11,
        12 => hv_x86_reg_t_HV_X86_R12,
        13 => hv_x86_reg_t_HV_X86_R13,
        14 => hv_x86_reg_t_HV_X86_R14,
        15 => hv_x86_reg_t_HV_X86_R15,
        _ => panic!("unexpected indexed register: {reg}"),
    }
}

fn map_insn_reg(reg: Register) -> u32 {
    match reg {
        Register::AL => hv_x86_reg_t_HV_X86_RAX,
        Register::CL => hv_x86_reg_t_HV_X86_RCX,
        Register::DL => hv_x86_reg_t_HV_X86_RDX,
        Register::BL => hv_x86_reg_t_HV_X86_RBX,
        Register::AH => hv_x86_reg_t_HV_X86_RAX,
        Register::CH => hv_x86_reg_t_HV_X86_RCX,
        Register::DH => hv_x86_reg_t_HV_X86_RDX,
        Register::BH => hv_x86_reg_t_HV_X86_RBX,
        Register::SPL => hv_x86_reg_t_HV_X86_RSP,
        Register::BPL => hv_x86_reg_t_HV_X86_RBP,
        Register::SIL => hv_x86_reg_t_HV_X86_RSI,
        Register::DIL => hv_x86_reg_t_HV_X86_RDI,
        Register::R8L => hv_x86_reg_t_HV_X86_R8,
        Register::R9L => hv_x86_reg_t_HV_X86_R9,
        Register::R10L => hv_x86_reg_t_HV_X86_R10,
        Register::R11L => hv_x86_reg_t_HV_X86_R11,
        Register::R12L => hv_x86_reg_t_HV_X86_R12,
        Register::R13L => hv_x86_reg_t_HV_X86_R13,
        Register::R14L => hv_x86_reg_t_HV_X86_R14,
        Register::R15L => hv_x86_reg_t_HV_X86_R15,

        Register::AX => hv_x86_reg_t_HV_X86_RAX,
        Register::CX => hv_x86_reg_t_HV_X86_RCX,
        Register::DX => hv_x86_reg_t_HV_X86_RDX,
        Register::BX => hv_x86_reg_t_HV_X86_RBX,
        Register::SP => hv_x86_reg_t_HV_X86_RSP,
        Register::BP => hv_x86_reg_t_HV_X86_RBP,
        Register::SI => hv_x86_reg_t_HV_X86_RSI,
        Register::DI => hv_x86_reg_t_HV_X86_RDI,
        Register::R8W => hv_x86_reg_t_HV_X86_R8,
        Register::R9W => hv_x86_reg_t_HV_X86_R9,
        Register::R10W => hv_x86_reg_t_HV_X86_R10,
        Register::R11W => hv_x86_reg_t_HV_X86_R11,
        Register::R12W => hv_x86_reg_t_HV_X86_R12,
        Register::R13W => hv_x86_reg_t_HV_X86_R13,
        Register::R14W => hv_x86_reg_t_HV_X86_R14,
        Register::R15W => hv_x86_reg_t_HV_X86_R15,

        Register::EAX => hv_x86_reg_t_HV_X86_RAX,
        Register::ECX => hv_x86_reg_t_HV_X86_RCX,
        Register::EDX => hv_x86_reg_t_HV_X86_RDX,
        Register::EBX => hv_x86_reg_t_HV_X86_RBX,
        Register::ESP => hv_x86_reg_t_HV_X86_RSP,
        Register::EBP => hv_x86_reg_t_HV_X86_RBP,
        Register::ESI => hv_x86_reg_t_HV_X86_RSI,
        Register::EDI => hv_x86_reg_t_HV_X86_RDI,
        Register::R8D => hv_x86_reg_t_HV_X86_R8,
        Register::R9D => hv_x86_reg_t_HV_X86_R9,
        Register::R10D => hv_x86_reg_t_HV_X86_R10,
        Register::R11D => hv_x86_reg_t_HV_X86_R11,
        Register::R12D => hv_x86_reg_t_HV_X86_R12,
        Register::R13D => hv_x86_reg_t_HV_X86_R13,
        Register::R14D => hv_x86_reg_t_HV_X86_R14,
        Register::R15D => hv_x86_reg_t_HV_X86_R15,

        Register::RAX => hv_x86_reg_t_HV_X86_RAX,
        Register::RCX => hv_x86_reg_t_HV_X86_RCX,
        Register::RDX => hv_x86_reg_t_HV_X86_RDX,
        Register::RBX => hv_x86_reg_t_HV_X86_RBX,
        Register::RSP => hv_x86_reg_t_HV_X86_RSP,
        Register::RBP => hv_x86_reg_t_HV_X86_RBP,
        Register::RSI => hv_x86_reg_t_HV_X86_RSI,
        Register::RDI => hv_x86_reg_t_HV_X86_RDI,
        Register::R8 => hv_x86_reg_t_HV_X86_R8,
        Register::R9 => hv_x86_reg_t_HV_X86_R9,
        Register::R10 => hv_x86_reg_t_HV_X86_R10,
        Register::R11 => hv_x86_reg_t_HV_X86_R11,
        Register::R12 => hv_x86_reg_t_HV_X86_R12,
        Register::R13 => hv_x86_reg_t_HV_X86_R13,
        Register::R14 => hv_x86_reg_t_HV_X86_R14,
        Register::R15 => hv_x86_reg_t_HV_X86_R15,

        Register::EIP => hv_x86_reg_t_HV_X86_RIP,
        Register::RIP => hv_x86_reg_t_HV_X86_RIP,

        Register::ES => hv_x86_reg_t_HV_X86_ES,
        Register::CS => hv_x86_reg_t_HV_X86_CS,
        Register::SS => hv_x86_reg_t_HV_X86_SS,
        Register::DS => hv_x86_reg_t_HV_X86_DS,
        Register::FS => hv_x86_reg_t_HV_X86_FS,
        Register::GS => hv_x86_reg_t_HV_X86_GS,

        _ => panic!("unexpected register: {:?}", reg),
    }
}

fn set_apic_delivery_mode(reg: u32, mode: u32) -> u32 {
    ((reg) & !0x700) | ((mode) << 8)
}

struct XstateInfo {
    offset: Option<u32>,
    size: u32,
    flags: u32,
}

fn get_xstate_info(feature_index: u32) -> XstateInfo {
    match feature_index {
        // FP
        0 => XstateInfo {
            offset: Some(0),
            size: 160,
            flags: 0,
        },
        // SSE
        1 => XstateInfo {
            offset: Some(160),
            size: 256,
            flags: 0,
        },
        _ => {
            // read from cpuid
            let res = unsafe { __cpuid_count(CPUID_XSTATE, feature_index) };
            let size = res.eax;
            let flags = res.ecx;
            let offset = if flags & XSTATE_FLAG_SUPERVISOR != 0 {
                // supervisor: offset is invalid because it's always compact format
                None
            } else {
                Some(res.ebx)
            };
            XstateInfo {
                offset,
                size,
                flags,
            }
        }
    }
}
