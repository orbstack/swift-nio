// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

#[allow(non_camel_case_types)]
#[allow(improper_ctypes)]
#[allow(dead_code)]
#[allow(non_snake_case)]
#[allow(non_upper_case_globals)]
#[allow(deref_nullptr)]
mod bindings;
use bindings::*;
use vm_memory::{Address, GuestMemory, GuestMemoryMmap};

use std::arch::asm;
use std::convert::TryInto;
use std::ffi::c_void;
use std::sync::atomic::{AtomicIsize, Ordering};
use std::sync::Arc;
use std::thread::Thread;
use std::time::Duration;

use crossbeam_channel::Sender;
use tracing::{debug, error};

use counter::RateCounter;

extern "C" {
    pub fn mach_absolute_time() -> u64;
}

counter::counter! {
    COUNTER_VMEX_TOTAL in "hvf.vmexit.total": RateCounter = RateCounter::new(FILTER);
}

const HV_EXIT_REASON_CANCELED: hv_exit_reason_t = 0;
const HV_EXIT_REASON_EXCEPTION: hv_exit_reason_t = 1;
const HV_EXIT_REASON_VTIMER_ACTIVATED: hv_exit_reason_t = 2;

const PSR_MODE_EL1H: u64 = 0x0000_0005;
const PSR_F_BIT: u64 = 0x0000_0040;
const PSR_I_BIT: u64 = 0x0000_0080;
const PSR_A_BIT: u64 = 0x0000_0100;
const PSR_D_BIT: u64 = 0x0000_0200;
const PSTATE_FAULT_BITS_64: u64 = PSR_MODE_EL1H | PSR_A_BIT | PSR_F_BIT | PSR_I_BIT | PSR_D_BIT;

const EC_WFX_TRAP: u64 = 0x1;
const EC_AA64_HVC: u64 = 0x16;
const EC_AA64_SMC: u64 = 0x17;
const EC_SYSTEMREGISTERTRAP: u64 = 0x18;
const EC_DATAABORT: u64 = 0x24;
const EC_AA64_BKPT: u64 = 0x3c;

const SYS_REG_SENTINEL: u64 = 0xb724_5c1e_68e7_5fc5;
const ACTLR_EL1_EN_TSO: u64 = 0x2;
static ACTLR_EL1_OFFSET: AtomicIsize = AtomicIsize::new(-1);

macro_rules! arm64_sys_reg {
    ($name: tt, $op0: tt, $op1: tt, $op2: tt, $crn: tt, $crm: tt) => {
        const $name: u64 = ($op0 as u64) << 20
            | ($op2 as u64) << 17
            | ($op1 as u64) << 14
            | ($crn as u64) << 10
            | ($crm as u64) << 1;
    };
}

arm64_sys_reg!(SYSREG_MASK, 0x3, 0x7, 0x7, 0xf, 0xf);

#[derive(thiserror::Error, Debug)]
pub enum Error {
    #[error("memory map")]
    MemoryMap,
    #[error("memory unmap")]
    MemoryUnmap,
    #[error("vcpu create")]
    VcpuCreate,
    #[error("vcpu initial registers")]
    VcpuInitialRegisters,
    #[error("vcpu read register")]
    VcpuReadRegister,
    #[error("vcpu read system register")]
    VcpuReadSystemRegister,
    #[error("vcpu request exit")]
    VcpuRequestExit,
    #[error("vcpu run")]
    VcpuRun,
    #[error("vcpu set pending irq")]
    VcpuSetPendingIrq,
    #[error("vcpu set register")]
    VcpuSetRegister,
    #[error("vcpu set system register")]
    VcpuSetSystemRegister,
    #[error("vcpu set vtimer mask")]
    VcpuSetVtimerMask,
    #[error("vm config set ipa size")]
    VmConfigSetIpaSize,
    #[error("vm create")]
    VmCreate,
    #[error("vm allocate")]
    VmAllocate,
    #[error("host CPU doesn't support assigning {0} bits of VM memory")]
    VmConfigIpaSizeLimit(u32),
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

pub fn vcpu_request_exit(vcpuid: u64) -> Result<(), Error> {
    let mut vcpu: u64 = vcpuid;
    let ret = unsafe { hv_vcpus_exit(&mut vcpu, 1) };

    if ret != HV_SUCCESS {
        Err(Error::VcpuRequestExit)
    } else {
        Ok(())
    }
}

pub fn vcpu_set_pending_irq(
    vcpuid: u64,
    irq_type: InterruptType,
    pending: bool,
) -> Result<(), Error> {
    let _type = match irq_type {
        InterruptType::Irq => hv_interrupt_type_t_HV_INTERRUPT_TYPE_IRQ,
        InterruptType::Fiq => hv_interrupt_type_t_HV_INTERRUPT_TYPE_FIQ,
    };

    let ret = unsafe { hv_vcpu_set_pending_interrupt(vcpuid, _type, pending) };

    if ret != HV_SUCCESS {
        Err(Error::VcpuSetPendingIrq)
    } else {
        Ok(())
    }
}

pub fn vcpu_set_vtimer_mask(vcpuid: u64, masked: bool) -> Result<(), Error> {
    let ret = unsafe { hv_vcpu_set_vtimer_mask(vcpuid, masked) };

    if ret != HV_SUCCESS {
        Err(Error::VcpuSetVtimerMask)
    } else {
        Ok(())
    }
}

pub type VcpuId = u64;

pub trait Parkable: Send + Sync {
    fn park(&self) -> Result<(), ParkError>;
    fn unpark(&self);
    fn before_vcpu_run(&self, vcpuid: u64);
    fn register_vcpu(&self, vcpuid: u64, wfe_thread: Thread);
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
    pub fn new(guest_mem: &GuestMemoryMmap) -> Result<Self, Error> {
        // safe: infallible
        let config = unsafe { hv_vm_config_create() };

        // how many IPA bits do we need? check highest guest mem address
        let ipa_bits = guest_mem.last_addr().raw_value().ilog2() + 1;
        debug!("IPA size: {} bits", ipa_bits);
        if ipa_bits > Self::get_default_ipa_size() {
            // if we need more than default, make sure HW supports it
            if ipa_bits > Self::get_max_ipa_size() {
                return Err(Error::VmConfigIpaSizeLimit(ipa_bits));
            }

            // it's supported. set it
            let ret = unsafe { hv_vm_config_set_ipa_size(config, ipa_bits) };
            if ret != HV_SUCCESS {
                return Err(Error::VmConfigSetIpaSize);
            }
        }

        let ret = unsafe { hv_vm_create(config) };
        if ret != HV_SUCCESS {
            Err(Error::VmCreate)
        } else {
            Ok(Self {})
        }
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

    pub fn force_exits(&self, vcpu_ids: &mut Vec<hv_vcpu_t>) -> Result<(), Error> {
        let ret = unsafe { hv_vcpus_exit(vcpu_ids.as_mut_ptr(), vcpu_ids.len() as u32) };
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

    fn get_default_ipa_size() -> u32 {
        let mut ipa_bit_length: u32 = 0;
        unsafe { hv_vm_config_get_default_ipa_size(&mut ipa_bit_length) };
        ipa_bit_length
    }

    fn get_max_ipa_size() -> u32 {
        let mut ipa_bit_length: u32 = 0;
        unsafe { hv_vm_config_get_max_ipa_size(&mut ipa_bit_length) };
        ipa_bit_length
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
    PvlockPark,
    PvlockUnpark(u64),
}

struct MmioRead {
    addr: u64,
    len: usize,
    srt: u32,
}

pub struct HvfVcpu<'a> {
    parker: Arc<dyn Parkable>,
    vcpuid: hv_vcpu_t,
    vcpu_exit: &'a hv_vcpu_exit_t,
    cntfrq: u64,
    mmio_buf: [u8; 8],
    pending_mmio_read: Option<MmioRead>,
    pending_advance_pc: bool,
}

extern "C" {
    pub fn _hv_vcpu_get_context(vcpu: hv_vcpu_t) -> *mut c_void;
}

// must be 8-byte aligned, so go in units of 8 bytes
fn search_8b_linear(haystack_ptr: *mut u64, needle: u64, haystack_bytes: usize) -> Option<usize> {
    let mut i = 0;
    while i < (haystack_bytes / 8) {
        if unsafe { *haystack_ptr.offset(i as isize) } == needle as u64 {
            return Some(i);
        }
        i += 1;
    }
    None
}

impl<'a> HvfVcpu<'a> {
    pub fn new(parker: Arc<dyn Parkable>) -> Result<Self, Error> {
        let mut vcpuid: hv_vcpu_t = 0;
        let mut vcpu_exit_ptr: *mut hv_vcpu_exit_t = std::ptr::null_mut();

        let cntfrq: u64;
        unsafe { asm!("mrs {}, cntfrq_el0", out(reg) cntfrq) };

        let ret = unsafe {
            hv_vcpu_create(
                &mut vcpuid,
                &mut vcpu_exit_ptr as *mut *mut _,
                std::ptr::null_mut(),
            )
        };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuCreate);
        }

        let vcpu_exit: &hv_vcpu_exit_t = unsafe { vcpu_exit_ptr.as_mut().unwrap() };

        Ok(Self {
            parker,
            vcpuid,
            vcpu_exit,
            cntfrq,
            mmio_buf: [0; 8],
            pending_mmio_read: None,
            pending_advance_pc: false,
        })
    }

    pub fn set_initial_state(
        &self,
        entry_addr: u64,
        fdt_addr: u64,
        mpidr: u64,
        enable_tso: bool,
    ) -> Result<(), Error> {
        self.write_raw_reg(hv_reg_t_HV_REG_CPSR, PSTATE_FAULT_BITS_64)?;
        self.write_raw_reg(hv_reg_t_HV_REG_PC, entry_addr)?;
        self.write_raw_reg(hv_reg_t_HV_REG_X0, fdt_addr)?;
        self.write_sys_reg(hv_sys_reg_t_HV_SYS_REG_MPIDR_EL1, mpidr)?;
        if enable_tso {
            self.write_tso_sys_reg(true)?;
        }
        Ok(())
    }

    pub fn id(&self) -> u64 {
        self.vcpuid
    }

    pub fn read_raw_reg(&self, reg: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_get_reg(self.vcpuid, reg, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadRegister)
        } else {
            Ok(val)
        }
    }

    pub fn write_raw_reg(&self, reg: u32, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_set_reg(self.vcpuid, reg, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetRegister)
        } else {
            Ok(())
        }
    }

    pub fn read_gp_reg(&self, reg: u32) -> Result<u64, Error> {
        assert!(reg < 32);

        if reg == 31 {
            Ok(0)
        } else {
            self.read_raw_reg(hv_reg_t_HV_REG_X0 + reg)
        }
    }

    pub fn write_gp_reg(&self, reg: u32, val: u64) -> Result<(), Error> {
        assert!(reg < 32);

        if reg == 31 {
            // ignore attempt to write to xzr
            Ok(())
        } else {
            self.write_raw_reg(hv_reg_t_HV_REG_X0 + reg, val)
        }
    }

    fn read_sys_reg(&self, reg: u16) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_get_sys_reg(self.vcpuid, reg, &mut val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuReadSystemRegister)
        } else {
            Ok(val)
        }
    }

    #[allow(dead_code)]
    fn write_sys_reg(&self, reg: u16, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_set_sys_reg(self.vcpuid, reg, val) };
        if ret != HV_SUCCESS {
            Err(Error::VcpuSetSystemRegister)
        } else {
            Ok(())
        }
    }

    fn write_tso_sys_reg(&self, en_tso: bool) -> Result<(), Error> {
        // get pointer to vcpu context struct for this vcpu
        // this is actually in a global array indexed by vcpuid
        let vcpu_ptr = unsafe { _hv_vcpu_get_context(self.vcpuid) };
        if vcpu_ptr.is_null() {
            return Err(Error::VcpuInitialRegisters);
        }

        // back up sctlr_el1
        let sctlr_el1 = self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_SCTLR_EL1)?;

        // search for sentinel starting at vcpu_ptr
        // since it's a linear array of all vcpus, there's probably at least 4096 bytes we can read
        // but ideally we'd have segfault recovery here
        // at least use linear search to be safe in case we might go out of bounds near the end
        // also: for perf and safety, we only have to do this once globally
        let mut actlr_el1_offset = ACTLR_EL1_OFFSET.load(Ordering::Relaxed);
        if actlr_el1_offset == -1 {
            // set sctlr_el1 to sentinel value
            self.write_sys_reg(hv_sys_reg_t_HV_SYS_REG_SCTLR_EL1, SYS_REG_SENTINEL)?;

            let sctlr_offset = search_8b_linear(vcpu_ptr as *mut u64, SYS_REG_SENTINEL, 4096)
                .ok_or(Error::VcpuInitialRegisters)?;
            // actlr_el1 (0xc081) has always been before sctlr_el1 (0xc080)
            // TODO: impossible to do this better? (setting all sysregs and finding holes doesn't work -- there are too many holes)
            actlr_el1_offset = sctlr_offset as isize * 8 - 8;
            ACTLR_EL1_OFFSET.store(actlr_el1_offset, Ordering::Relaxed);
        }

        let actlr_el1_ptr = unsafe { vcpu_ptr.offset(actlr_el1_offset) as *mut u64 };

        // set EN_TSO to 1 (enable TSO) on ACTLR_EL1
        unsafe {
            if en_tso {
                *actlr_el1_ptr |= ACTLR_EL1_EN_TSO;
            } else {
                *actlr_el1_ptr &= !ACTLR_EL1_EN_TSO;
            }
        }

        // restore sctlr_el1 to original value
        // this should also flag regs as dirty
        self.write_sys_reg(hv_sys_reg_t_HV_SYS_REG_SCTLR_EL1, sctlr_el1)?;

        Ok(())
    }

    pub fn run(&mut self, pending_irq: bool) -> Result<VcpuExit, Error> {
        self.parker.before_vcpu_run(self.vcpuid);

        if self.parker.should_shutdown() {
            return Ok(VcpuExit::Shutdown);
        }

        if let Some(mmio_read) = self.pending_mmio_read.take() {
            if mmio_read.srt < 31 {
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

                self.write_raw_reg(hv_reg_t_HV_REG_X0 + mmio_read.srt, val)?;
            }
        }

        if self.pending_advance_pc {
            let pc = self.read_raw_reg(hv_reg_t_HV_REG_PC)?;
            self.write_raw_reg(hv_reg_t_HV_REG_PC, pc + 4)?;
            self.pending_advance_pc = false;
        }

        if pending_irq {
            vcpu_set_pending_irq(self.vcpuid, InterruptType::Irq, true)?;
        }

        let ret = unsafe { hv_vcpu_run(self.vcpuid) };
        if ret != HV_SUCCESS {
            return Err(Error::VcpuRun);
        }

        COUNTER_VMEX_TOTAL.count();

        match self.vcpu_exit.reason {
            HV_EXIT_REASON_CANCELED => Ok(VcpuExit::Canceled),
            HV_EXIT_REASON_EXCEPTION => {
                let syndrome = self.vcpu_exit.exception.syndrome;
                let ec = (syndrome >> 26) & 0x3f;

                match ec {
                    EC_AA64_HVC => {
                        let val = self.read_raw_reg(hv_reg_t_HV_REG_X0)?;

                        debug!("HVC: 0x{:x}", val);
                        let ret = match val {
                            0x8400_0000 => Some(2),
                            0x8400_0006 => Some(2),
                            0x8400_0008 | 0x8400_0009 => return Ok(VcpuExit::Shutdown),
                            0xc400_0003 => {
                                let mpidr = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                                let entry = self.read_raw_reg(hv_reg_t_HV_REG_X2)?;
                                let context_id = self.read_raw_reg(hv_reg_t_HV_REG_X3)?;
                                self.write_raw_reg(hv_reg_t_HV_REG_X0, 0)?;
                                return Ok(VcpuExit::CpuOn(mpidr, entry, context_id));
                            }
                            0xc400_002a => {
                                let dev_id = self.read_raw_reg(hv_reg_t_HV_REG_X1)? as usize;
                                let args_ptr = self.read_raw_reg(hv_reg_t_HV_REG_X2)? as usize;
                                return Ok(VcpuExit::HypervisorIoCall { dev_id, args_ptr });
                            }
                            0xc300_0005 => {
                                return Ok(VcpuExit::PvlockPark);
                            }
                            0xc300_0006 => {
                                let vcpuid = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                                return Ok(VcpuExit::PvlockUnpark(vcpuid));
                            }
                            _ => {
                                debug!("HVC call unhandled");
                                None
                            }
                        };

                        if let Some(ret) = ret {
                            self.write_raw_reg(hv_reg_t_HV_REG_X0, ret)?;
                        }

                        Ok(VcpuExit::HypervisorCall)
                    }
                    EC_AA64_SMC => {
                        debug!("SMC exit");

                        self.pending_advance_pc = true;
                        Ok(VcpuExit::SecureMonitorCall)
                    }
                    EC_SYSTEMREGISTERTRAP => {
                        let is_read: bool = (syndrome & 1) != 0;
                        let arg_reg_idx: u32 = ((syndrome >> 5) & 0x1f) as u32;
                        let sys_reg: u64 = syndrome & SYSREG_MASK;

                        tracing::debug!("sysreg operation reg={} target={arg_reg_idx} (op0={} op1={} op2={} crn={} crm={}) isread={:?}",
                               sys_reg, (sys_reg >> 20) & 0x3,
                               (sys_reg >> 14) & 0x7, (sys_reg >> 17) & 0x7,
                               (sys_reg >> 10) & 0xf, (sys_reg >> 1) & 0xf,
                               is_read);

                        self.pending_advance_pc = true;
                        Ok(VcpuExit::SystemRegister {
                            sys_reg,
                            arg_reg_idx,
                            is_read,
                        })
                    }
                    EC_DATAABORT => {
                        let isv: bool = (syndrome & (1 << 24)) != 0;
                        let iswrite: bool = ((syndrome >> 6) & 1) != 0;
                        let s1ptw: bool = ((syndrome >> 7) & 1) != 0;
                        let sas: u32 = (syndrome as u32 >> 22) & 3;
                        let len: usize = (1 << sas) as usize;
                        let srt: u32 = (syndrome as u32 >> 16) & 0x1f;

                        debug!("data abort: va={:x}, pa={:x}, isv={}, iswrite={:?}, s1ptrw={}, len={}, srt={}",
                               self.vcpu_exit.exception.virtual_address,
                               self.vcpu_exit.exception.physical_address,
                               isv, iswrite, s1ptw, len, srt);

                        let pa = self.vcpu_exit.exception.physical_address;
                        self.pending_advance_pc = true;

                        if iswrite {
                            let val = if srt < 31 {
                                self.read_raw_reg(hv_reg_t_HV_REG_X0 + srt)?
                            } else {
                                0u64
                            };

                            match len {
                                1 => {
                                    self.mmio_buf[0..1].copy_from_slice(&(val as u8).to_le_bytes())
                                }
                                4 => {
                                    self.mmio_buf[0..4].copy_from_slice(&(val as u32).to_le_bytes())
                                }
                                8 => self.mmio_buf[0..8].copy_from_slice(&(val).to_le_bytes()),
                                _ => panic!("unsupported mmio len={len}"),
                            };

                            Ok(VcpuExit::MmioWrite(pa, &self.mmio_buf[0..len]))
                        } else {
                            self.pending_mmio_read = Some(MmioRead { addr: pa, srt, len });
                            Ok(VcpuExit::MmioRead(pa, &mut self.mmio_buf[0..len]))
                        }
                    }
                    EC_AA64_BKPT => {
                        debug!("BRK exit");
                        Ok(VcpuExit::Breakpoint)
                    }
                    EC_WFX_TRAP => {
                        debug!("WFX exit");
                        let ctl = self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_CNTV_CTL_EL0)?;

                        self.pending_advance_pc = true;
                        if ((ctl & 1) == 0) || (ctl & 2) != 0 {
                            Ok(VcpuExit::WaitForEvent)
                        } else {
                            let cval = self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_CNTV_CVAL_EL0)?;
                            let now = unsafe { mach_absolute_time() };

                            if now > cval {
                                Ok(VcpuExit::WaitForEventExpired)
                            } else {
                                let timeout = Duration::from_nanos(
                                    (cval - now) * (1_000_000_000 / self.cntfrq),
                                );
                                Ok(VcpuExit::WaitForEventTimeout(timeout))
                            }
                        }
                    }
                    _ => panic!("unexpected exception: 0x{ec:x}"),
                }
            }
            HV_EXIT_REASON_VTIMER_ACTIVATED => Ok(VcpuExit::VtimerActivated),
            _ => {
                let pc = self.read_raw_reg(hv_reg_t_HV_REG_PC)?;
                panic!(
                    "unexpected exit reason: vcpuid={} 0x{:x} at pc=0x{:x}",
                    self.id(),
                    self.vcpu_exit.reason,
                    pc
                );
            }
        }
    }

    pub fn destroy(self) {
        let err = unsafe { hv_vcpu_destroy(self.vcpuid) };
        if err != 0 {
            error!("Failed to destroy vcpu: {err}");
        }
    }
}

pub fn vcpu_read_mpidr(vcpu_id: u64) -> Result<u64, Error> {
    let mut val: u64 = 0;
    let ret = unsafe { hv_vcpu_get_sys_reg(vcpu_id, hv_sys_reg_t_HV_SYS_REG_MPIDR_EL1, &mut val) };

    if ret != HV_SUCCESS {
        Err(Error::VcpuReadSystemRegister)
    } else {
        Ok(val)
    }
}

pub unsafe fn vm_allocate(size: usize) -> Result<*mut c_void, Error> {
    let mut ptr: *mut c_void = std::ptr::null_mut();
    let ret = unsafe { hv_vm_allocate(&mut ptr, size, HV_ALLOCATE_DEFAULT as u64) };
    if ret != HV_SUCCESS {
        Err(Error::VmAllocate)
    } else {
        Ok(ptr)
    }
}

pub unsafe fn vm_deallocate(ptr: *mut c_void, size: usize) -> Result<(), Error> {
    let ret = unsafe { hv_vm_deallocate(ptr, size) };
    if ret != HV_SUCCESS {
        Err(Error::VmAllocate)
    } else {
        Ok(())
    }
}
