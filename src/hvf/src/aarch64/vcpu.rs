// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

use anyhow::anyhow;
use arch::aarch64::layout::DRAM_MEM_START;
use smallvec::SmallVec;
use utils::extract_bits_64;
use utils::kernel_symbols::CompactSystemMap;
use utils::mach_time::MachAbsoluteTime;
use utils::memory::GuestMemoryExt;
use vm_memory::{GuestAddress, GuestMemoryMmap};

use std::convert::TryInto;
use std::fmt::Write;
use std::sync::atomic::{AtomicIsize, Ordering};
use std::sync::Arc;

use tracing::{debug, error};

use counter::RateCounter;

use utils::hypercalls::{
    OrbvmFeatures, ORBVM_FEATURES, ORBVM_IO_REQUEST, ORBVM_MMIO_WRITE32, ORBVM_PVGIC_SET_STATE,
    ORBVM_PVLOCK_KICK, ORBVM_PVLOCK_WFK, ORBVM_SET_ACTLR_EL1, PSCI_CPU_ON, PSCI_MIGRATE_TYPE,
    PSCI_POWER_OFF, PSCI_RESET, PSCI_VERSION,
};

use crate::aarch64::bindings::{
    hv_reg_t_HV_REG_X1, hv_reg_t_HV_REG_X2, hv_reg_t_HV_REG_X3,
    hv_sys_reg_t_HV_SYS_REG_CNTV_CTL_EL0, hv_sys_reg_t_HV_SYS_REG_CNTV_CVAL_EL0,
};
use crate::aarch64::vm::USE_HVF_GIC;
use crate::profiler::arch::{is_hypercall_insn, ARM64_INSN_SIZE};
use crate::profiler::symbolicator::{
    HostKernelSymbolicator, LinuxSymbolicator, SymbolFunc, Symbolicator,
};
use crate::profiler::{Frame, FrameCategory, PartialSample, STACK_DEPTH_LIMIT};
use crate::VcpuProfilerState;

use super::bindings::{
    hv_exit_reason_t_HV_EXIT_REASON_CANCELED, hv_exit_reason_t_HV_EXIT_REASON_EXCEPTION,
    hv_exit_reason_t_HV_EXIT_REASON_VTIMER_ACTIVATED, hv_interrupt_type_t_HV_INTERRUPT_TYPE_FIQ,
    hv_interrupt_type_t_HV_INTERRUPT_TYPE_IRQ, hv_reg_t, hv_reg_t_HV_REG_CPSR, hv_reg_t_HV_REG_FP,
    hv_reg_t_HV_REG_LR, hv_reg_t_HV_REG_PC, hv_reg_t_HV_REG_X0, hv_sys_reg_t,
    hv_sys_reg_t_HV_SYS_REG_CONTEXTIDR_EL1, hv_sys_reg_t_HV_SYS_REG_ID_AA64MMFR0_EL1,
    hv_sys_reg_t_HV_SYS_REG_MPIDR_EL1, hv_sys_reg_t_HV_SYS_REG_SCTLR_EL1,
    hv_sys_reg_t_HV_SYS_REG_SP_EL1, hv_sys_reg_t_HV_SYS_REG_TCR_EL1,
    hv_sys_reg_t_HV_SYS_REG_TPIDR_EL1, hv_sys_reg_t_HV_SYS_REG_TTBR1_EL1,
    hv_sys_reg_t_HV_SYS_REG_VBAR_EL1, hv_vcpu_create, hv_vcpu_destroy, hv_vcpu_exit_t,
    hv_vcpu_get_reg, hv_vcpu_get_sys_reg, hv_vcpu_run, hv_vcpu_set_pending_interrupt,
    hv_vcpu_set_reg, hv_vcpu_set_sys_reg, hv_vcpu_set_vtimer_mask, hv_vcpu_t, hv_vcpus_exit,
};
use super::private::_hv_vcpu_get_context;
use super::pvgic::{ExitActions, PvgicFlags, PvgicVcpuState};
use super::vm::ENABLE_NESTED_VIRT;
use super::{Error, HvfError, HvfVm};

// kernel VA space is up to 48 bits on arm64. the EL1 split has high bits set,
// so any address without the top 16 bits set isn't a valid kernel address
const VA48_MASK: u64 = !(u64::MAX >> 16);

counter::counter! {
    COUNT_EXIT_TOTAL in "hvf.vmexit.total": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_HVC_ACTLR in "hvf.vmexit.hvc.actlr": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_HVC_VIRTIOFS in "hvf.vmexit.hvc.virtiofs": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_HVC_PVLOCK_WAIT in "hvf.vmexit.hvc.pvlock.wait": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_HVC_PVLOCK_KICK in "hvf.vmexit.hvc.pvlock.kick": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_MMIO_READ in "hvf.vmexit.mmio.read": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_MMIO_WRITE in "hvf.vmexit.mmio.write": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_SYSREG in "hvf.vmexit.sysreg": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_WFE_INDEFINITE in "hvf.vmexit.wfe.indefinite": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_WFE_TIMED in "hvf.vmexit.wfe.timed": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_VTIMER in "hvf.vmexit.vtimer": RateCounter = RateCounter::new(FILTER);
}

const PSR_MODE_EL0T: u64 = 0x0000_0000;
const PSR_MODE_EL1T: u64 = 0x0000_0004;
const PSR_MODE_EL1H: u64 = 0x0000_0005;
const PSR_MODE_EL2T: u64 = 0x0000_0008;
const PSR_MODE_EL2H: u64 = 0x0000_0009;
const PSR_MODE_MASK: u64 = 0x0000_000f;

const PSR_F_BIT: u64 = 0x0000_0040;
const PSR_I_BIT: u64 = 0x0000_0080;
const PSR_A_BIT: u64 = 0x0000_0100;
const PSR_D_BIT: u64 = 0x0000_0200;
const INITIAL_PSTATE: u64 = PSR_A_BIT | PSR_F_BIT | PSR_I_BIT | PSR_D_BIT;

const EC_WFX_TRAP: u64 = 0x1;
const EC_AA64_HVC: u64 = 0x16;
const EC_AA64_SMC: u64 = 0x17;
const EC_IABT_LOW: u64 = 0x20;
const EC_SYSTEMREGISTERTRAP: u64 = 0x18;
const EC_DATAABORT: u64 = 0x24;
const EC_AA64_BKPT: u64 = 0x3c;

const SYS_REG_SENTINEL: u64 = 0xb724_5c1e_68e7_5fc5;
// VZF seems to set either 0x202 (for Rosetta) or 0, but no one knows what 0x200 does
const ACTLR_EL1_EN_TSO: u64 = 1 << 1;
const ACTLR_EL1_MYSTERY: u64 = 0x200;
// only allow guest to set these values
const ACTLR_EL1_ALLOWED_MASK: u64 = ACTLR_EL1_EN_TSO | ACTLR_EL1_MYSTERY;
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

#[repr(u32)]
pub enum InterruptType {
    Irq = hv_interrupt_type_t_HV_INTERRUPT_TYPE_IRQ,
    Fiq = hv_interrupt_type_t_HV_INTERRUPT_TYPE_FIQ,
}

pub struct VcpuId(pub u64);

impl VcpuId {
    pub fn to_mpidr(&self) -> u64 {
        self.0 << 8
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
        args_addr: GuestAddress,
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
    WaitForEventDeadline(MachAbsoluteTime),
    PvlockPark,
    PvlockUnpark(u64),
}

struct MmioRead {
    addr: u64,
    len: usize,
    srt: u32,
}

#[derive(Debug, Clone, Copy)]
pub struct HvVcpuRef(hv_vcpu_t);

pub struct HvfVcpu {
    hv_vcpu: HvVcpuRef,
    vcpu_exit_ptr: *mut hv_vcpu_exit_t,
    mmio_buf: [u8; 8],
    pending_mmio_read: Option<MmioRead>,
    pending_advance_pc: bool,

    allow_actlr: bool,
    actlr_el1_ptr: *mut u64,

    guest_mem: GuestMemoryMmap,
    pvgic: Option<*mut PvgicVcpuState>,

    _hvf_vm: Arc<HvfVm>,
}

impl HvfVcpu {
    pub fn new(guest_mem: GuestMemoryMmap, hvf_vm: Arc<HvfVm>) -> Result<Self, Error> {
        let mut vcpuid: hv_vcpu_t = 0;
        let mut vcpu_exit_ptr: *mut hv_vcpu_exit_t = std::ptr::null_mut();

        let ret = unsafe {
            hv_vcpu_create(
                &mut vcpuid,
                &mut vcpu_exit_ptr as *mut *mut _,
                std::ptr::null_mut(),
            )
        };
        HvfError::result(ret).map_err(Error::VcpuCreate)?;

        Ok(Self {
            hv_vcpu: HvVcpuRef(vcpuid),
            vcpu_exit_ptr,
            mmio_buf: [0; 8],
            pending_mmio_read: None,
            pending_advance_pc: false,

            allow_actlr: false,
            actlr_el1_ptr: std::ptr::null_mut(),

            guest_mem,
            pvgic: None,

            _hvf_vm: hvf_vm,
        })
    }

    pub fn set_initial_state(
        &mut self,
        entry_addr: u64,
        fdt_addr: u64,
        mpidr: u64,
        enable_tso: bool,
    ) -> Result<(), Error> {
        // enable TSO first. this breaks after setting CPSR to EL2
        if enable_tso {
            self.allow_actlr = true;
            self.write_actlr_el1(ACTLR_EL1_MYSTERY)?;
        }

        let initial_el = if ENABLE_NESTED_VIRT {
            PSR_MODE_EL2H
        } else {
            PSR_MODE_EL1H
        };
        self.write_raw_reg(hv_reg_t_HV_REG_CPSR, INITIAL_PSTATE | initial_el)?;

        self.write_raw_reg(hv_reg_t_HV_REG_PC, entry_addr)?;
        self.write_raw_reg(hv_reg_t_HV_REG_X0, fdt_addr)?;
        self.write_raw_reg(hv_reg_t_HV_REG_X1, 0)?;
        self.write_raw_reg(hv_reg_t_HV_REG_X2, 0)?;
        self.write_raw_reg(hv_reg_t_HV_REG_X3, 0)?;
        self.write_sys_reg(hv_sys_reg_t_HV_SYS_REG_MPIDR_EL1, mpidr)?;

        Ok(())
    }

    pub fn id(&self) -> u64 {
        // TODO: hv_vcpu_t vs. vcpu index hygiene
        self.hv_vcpu.0
    }

    pub fn vcpu_ref(&self) -> HvVcpuRef {
        self.hv_vcpu
    }

    pub fn read_raw_reg(&self, reg: hv_reg_t) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_get_reg(self.hv_vcpu.0, reg, &mut val) };
        HvfError::result(ret).map_err(Error::VcpuReadRegister)?;
        Ok(val)
    }

    pub fn write_raw_reg(&mut self, reg: hv_reg_t, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_set_reg(self.hv_vcpu.0, reg, val) };
        HvfError::result(ret).map_err(Error::VcpuSetRegister)
    }

    pub fn read_gp_reg(&self, reg: u32) -> Result<u64, Error> {
        assert!(reg < 32);

        if reg == 31 {
            Ok(0)
        } else {
            self.read_raw_reg(hv_reg_t_HV_REG_X0 + reg)
        }
    }

    pub fn write_gp_reg(&mut self, reg: u32, val: u64) -> Result<(), Error> {
        assert!(reg < 32);

        if reg == 31 {
            // ignore attempt to write to xzr
            Ok(())
        } else {
            self.write_raw_reg(hv_reg_t_HV_REG_X0 + reg, val)
        }
    }

    fn read_sys_reg(&self, reg: hv_sys_reg_t) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_get_sys_reg(self.hv_vcpu.0, reg, &mut val) };
        HvfError::result(ret).map_err(Error::VcpuReadSystemRegister)?;
        Ok(val)
    }

    fn write_sys_reg(&mut self, reg: hv_sys_reg_t, val: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_set_sys_reg(self.hv_vcpu.0, reg, val) };
        HvfError::result(ret).map_err(Error::VcpuSetSystemRegister)
    }

    fn write_actlr_el1(&mut self, new_value: u64) -> Result<(), Error> {
        let actlr_el1_ptr = self.actlr_el1_ptr;
        if actlr_el1_ptr.is_null() {
            return self.write_actlr_el1_initial(new_value);
        }

        // fastpath
        // flag regs as dirty via unused sysreg where value doesn't matter
        self.write_sys_reg(hv_sys_reg_t_HV_SYS_REG_CONTEXTIDR_EL1, 0)?;

        // write this *after* potentially syncing from hv (which would overwrite it)
        unsafe { actlr_el1_ptr.write_volatile(new_value) };
        Ok(())
    }

    fn write_actlr_el1_initial(&mut self, new_value: u64) -> Result<(), Error> {
        // get pointer to vcpu context struct for this vcpu
        // this is actually in a global array indexed by vcpuid
        let vcpu_ptr = unsafe { _hv_vcpu_get_context(self.hv_vcpu.0) };
        if vcpu_ptr.is_null() {
            return Err(Error::VcpuInitialRegisters(HvfError::Unknown));
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

            let sctlr_offset = unsafe {
                search_8b_linear(vcpu_ptr as *mut u64, SYS_REG_SENTINEL, 4096)
                    .ok_or(Error::VcpuInitialRegisters(HvfError::Unknown))?
            };
            // actlr_el1 (0xc081) has always been before sctlr_el1 (0xc080)
            // TODO: impossible to do this better? (setting all sysregs and finding holes doesn't work -- there are too many holes)
            actlr_el1_offset = sctlr_offset as isize * 8 - 8;
            ACTLR_EL1_OFFSET.store(actlr_el1_offset, Ordering::Relaxed);
        }

        // restore sctlr_el1 to original value
        // this should also flag regs as dirty
        self.write_sys_reg(hv_sys_reg_t_HV_SYS_REG_SCTLR_EL1, sctlr_el1)?;

        // write this *after* potentially syncing from hv (which would overwrite it)
        let actlr_el1_ptr = unsafe { vcpu_ptr.offset(actlr_el1_offset) as *mut u64 };
        self.actlr_el1_ptr = actlr_el1_ptr;
        unsafe { actlr_el1_ptr.write_volatile(new_value) };

        Ok(())
    }

    pub fn run(&mut self, pending_irq: Option<u32>) -> Result<(VcpuExit, ExitActions), Error> {
        let mut exit_actions = ExitActions::empty();

        // .take() is slower
        if let Some(mmio_read) = &self.pending_mmio_read {
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

            self.pending_mmio_read = None;
        }

        if self.pending_advance_pc {
            let pc = self.read_raw_reg(hv_reg_t_HV_REG_PC)?;
            self.write_raw_reg(hv_reg_t_HV_REG_PC, pc + 4)?;
            self.pending_advance_pc = false;
        }

        if let Some(pending_irq) = pending_irq {
            Self::set_pending_irq(self.hv_vcpu, InterruptType::Irq, true)?;

            if let Some(pvgic_ptr) = self.pvgic {
                let pvgic = unsafe { &mut *pvgic_ptr };
                // if there's a pending IRQ, IAR1_EL1 always has a valid value (!= 1023)
                pvgic.flags = PvgicFlags::IAR1_PENDING;
                pvgic.pending_iar1_read = pending_irq;
            }
        }

        // this FFI call acts as a barrier for pvgic read/write
        let ret = unsafe { hv_vcpu_run(self.hv_vcpu.0) };
        HvfError::result(ret).map_err(Error::VcpuRun)?;

        COUNT_EXIT_TOTAL.count();

        if pending_irq.is_some() {
            if let Some(pvgic_ptr) = self.pvgic {
                let pvgic = unsafe { &*pvgic_ptr };
                if pvgic.flags.contains(PvgicFlags::IAR1_READ) {
                    // we can only return one vmexit here, so tell the emulation loop to trigger IAR1_EL1 read for side effects (dequeue)
                    // usually this will happen when the guest hits EOIR_EL1 write
                    exit_actions.insert(ExitActions::READ_IAR1_EL1);
                }
            }
        }

        let vcpu_exit = unsafe { &*self.vcpu_exit_ptr };
        #[allow(non_upper_case_globals)]
        let exit = match vcpu_exit.reason {
            hv_exit_reason_t_HV_EXIT_REASON_CANCELED => VcpuExit::Canceled,
            hv_exit_reason_t_HV_EXIT_REASON_EXCEPTION => {
                let syndrome = vcpu_exit.exception.syndrome;
                let ec = (syndrome >> 26) & 0x3f;

                match ec {
                    EC_AA64_HVC => self.handle_hvc()?,
                    EC_AA64_SMC => {
                        debug!("SMC exit");

                        self.pending_advance_pc = true;
                        VcpuExit::SecureMonitorCall
                    }
                    EC_IABT_LOW => {
                        panic!(
                            "instruction abort: syndrome={:x} va={:x} pa={:x}",
                            syndrome,
                            vcpu_exit.exception.virtual_address,
                            vcpu_exit.exception.physical_address
                        );
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

                        COUNT_EXIT_SYSREG.count();
                        self.pending_advance_pc = true;
                        VcpuExit::SystemRegister {
                            sys_reg,
                            arg_reg_idx,
                            is_read,
                        }
                    }
                    EC_DATAABORT => {
                        let isv: bool = (syndrome & (1 << 24)) != 0;
                        let iswrite: bool = ((syndrome >> 6) & 1) != 0;
                        let s1ptw: bool = ((syndrome >> 7) & 1) != 0;
                        let sas: u32 = (syndrome as u32 >> 22) & 3;
                        let len: usize = (1 << sas) as usize;
                        let srt: u32 = (syndrome as u32 >> 16) & 0x1f;

                        debug!("data abort: va={:x}, pa={:x}, isv={}, iswrite={:?}, s1ptrw={}, len={}, srt={}",
                               vcpu_exit.exception.virtual_address,
                               vcpu_exit.exception.physical_address,
                               isv, iswrite, s1ptw, len, srt);

                        let pa = vcpu_exit.exception.physical_address;
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

                            COUNT_EXIT_MMIO_WRITE.count();
                            VcpuExit::MmioWrite(pa, &self.mmio_buf[0..len])
                        } else {
                            COUNT_EXIT_MMIO_READ.count();
                            self.pending_mmio_read = Some(MmioRead { addr: pa, srt, len });
                            VcpuExit::MmioRead(pa, &mut self.mmio_buf[0..len])
                        }
                    }
                    EC_AA64_BKPT => {
                        debug!("BRK exit");
                        VcpuExit::Breakpoint
                    }

                    EC_WFX_TRAP => {
                        debug!("WFX exit");
                        let ctl = self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_CNTV_CTL_EL0)?;

                        self.pending_advance_pc = true;
                        if ((ctl & 1) == 0) || (ctl & 2) != 0 {
                            COUNT_EXIT_WFE_INDEFINITE.count();
                            VcpuExit::WaitForEvent
                        } else {
                            let deadline =
                                self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_CNTV_CVAL_EL0)?;
                            COUNT_EXIT_WFE_TIMED.count();
                            VcpuExit::WaitForEventDeadline(MachAbsoluteTime::from_raw(deadline))
                        }
                    }

                    _ => panic!("unexpected exception: 0x{ec:x}"),
                }
            }

            hv_exit_reason_t_HV_EXIT_REASON_VTIMER_ACTIVATED => {
                COUNT_EXIT_VTIMER.count();
                VcpuExit::VtimerActivated
            }

            _ => {
                let pc = self.read_raw_reg(hv_reg_t_HV_REG_PC)?;
                panic!(
                    "unexpected exit reason: vcpuid={} 0x{:x} at pc=0x{:x}",
                    self.id(),
                    vcpu_exit.reason,
                    pc
                );
            }
        };

        Ok((exit, exit_actions))
    }

    pub fn clear_pending_mmio(&mut self) {
        self.pending_mmio_read = None;
        self.pending_advance_pc = false;
    }

    fn handle_hvc(&mut self) -> Result<VcpuExit, Error> {
        let val = self.read_raw_reg(hv_reg_t_HV_REG_X0)?;

        debug!("HVC: 0x{:x}", val);
        let ret = match val as u32 {
            PSCI_VERSION => Some(2),
            PSCI_MIGRATE_TYPE => Some(2),
            PSCI_POWER_OFF | PSCI_RESET => return Ok(VcpuExit::Shutdown),

            PSCI_CPU_ON => {
                let mpidr = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                let entry = self.read_raw_reg(hv_reg_t_HV_REG_X2)?;
                let context_id = self.read_raw_reg(hv_reg_t_HV_REG_X3)?;
                self.write_raw_reg(hv_reg_t_HV_REG_X0, 0)?;
                return Ok(VcpuExit::CpuOn(mpidr, entry, context_id));
            }

            ORBVM_FEATURES => {
                // SMCCC default return value = -1, but faulty implementations might leave x0 unchanged or set x0=0
                // this makes it unambiguous
                let mask = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                let supported = OrbvmFeatures::all();
                Some(supported.bits() & mask)
            }

            ORBVM_IO_REQUEST => {
                COUNT_EXIT_HVC_VIRTIOFS.count();
                let dev_id = self.read_raw_reg(hv_reg_t_HV_REG_X1)? as usize;
                let args_addr = GuestAddress(self.read_raw_reg(hv_reg_t_HV_REG_X2)?);
                return Ok(VcpuExit::HypervisorIoCall { dev_id, args_addr });
            }

            ORBVM_PVGIC_SET_STATE => {
                if USE_HVF_GIC {
                    None
                } else {
                    let pvgic_state_addr = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                    let ptr = self
                        .guest_mem
                        .get_obj_ptr_aligned(GuestAddress(pvgic_state_addr))
                        .map_err(|_| Error::GetGuestMemory)?;
                    self.pvgic = Some(ptr);
                    Some(0)
                }
            }

            ORBVM_SET_ACTLR_EL1 => {
                COUNT_EXIT_HVC_ACTLR.count();

                if self.allow_actlr {
                    let value = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                    self.write_actlr_el1(value & ACTLR_EL1_ALLOWED_MASK)?;
                }

                return Ok(VcpuExit::HypervisorCall);
            }

            ORBVM_PVLOCK_WFK => {
                COUNT_EXIT_HVC_PVLOCK_WAIT.count();
                return Ok(VcpuExit::PvlockPark);
            }

            ORBVM_PVLOCK_KICK => {
                COUNT_EXIT_HVC_PVLOCK_KICK.count();
                let vcpuid = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                return Ok(VcpuExit::PvlockUnpark(vcpuid));
            }

            ORBVM_MMIO_WRITE32 => {
                let pa = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                let val = self.read_raw_reg(hv_reg_t_HV_REG_X2)? as u32;

                self.mmio_buf[0..4].copy_from_slice(&val.to_le_bytes());

                COUNT_EXIT_MMIO_WRITE.count();
                return Ok(VcpuExit::MmioWrite(pa, &self.mmio_buf[0..4]));
            }

            _ => {
                panic!("unhandled HVC: 0x{:x}", val);
            }
        };

        // SMCCC not supported
        self.write_raw_reg(hv_reg_t_HV_REG_X0, ret.unwrap_or(-1i64 as u64))?;
        Ok(VcpuExit::HypervisorCall)
    }

    // from cloud-hypervisor: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/29675cfe687dde124dd71ccaf31c0562938f1564/vmm/src/cpu.rs#L1670C66-L1818C6
    // license: Copyright © 2020, Oracle and/or its affiliates. Apache-2.0 AND BSD-3-Clause
    fn translate_gva(&self, gva: u64) -> Result<GuestAddress, Error> {
        let tcr_el1: u64 = self
            .read_sys_reg(hv_sys_reg_t_HV_SYS_REG_TCR_EL1)
            .map_err(|_| Error::TranslateVirtualAddress)?;
        let ttbr1_el1: u64 = self
            .read_sys_reg(hv_sys_reg_t_HV_SYS_REG_TTBR1_EL1)
            .map_err(|_| Error::TranslateVirtualAddress)?;
        let id_aa64mmfr0_el1: u64 = self
            .read_sys_reg(hv_sys_reg_t_HV_SYS_REG_ID_AA64MMFR0_EL1)
            .map_err(|_| Error::TranslateVirtualAddress)?;

        // Bit 55 of the VA determines the range, high (0xFFFxxx...)
        // or low (0x000xxx...).
        let high_range = extract_bits_64!(gva, 55, 1);
        if high_range == 0 {
            error!("VA (0x{:x}) range is not supported!", gva);
            return Ok(GuestAddress(gva));
        }

        // High range size offset
        let tsz = extract_bits_64!(tcr_el1, 16, 6);
        // Granule size
        let tg = extract_bits_64!(tcr_el1, 30, 2);
        // Indication of 48-bits (0) or 52-bits (1) for FEAT_LPA2
        let ds = extract_bits_64!(tcr_el1, 59, 1);

        if tsz == 0 {
            error!("VA translation is not ready!");
            return Ok(GuestAddress(gva));
        }

        // VA size is determined by TCR_BL1.T1SZ
        let va_size = 64 - tsz;
        // Number of bits in VA consumed in each level of translation
        let stride = match tg {
            3 => 13, // 64KB granule size
            1 => 11, // 16KB granule size
            _ => 9,  // 4KB, default
        };
        // Starting level of walking
        let mut level = 4 - (va_size - 4) / stride;

        // PA or IPA size is determined
        let tcr_ips = extract_bits_64!(tcr_el1, 32, 3);
        let pa_range = extract_bits_64!(id_aa64mmfr0_el1, 0, 4);
        // The IPA size in TCR_BL1 and PA Range in ID_AA64MMFR0_EL1 should match.
        // To be safe, we use the minimum value if they are different.
        let pa_range = std::cmp::min(tcr_ips, pa_range);
        // PA size in bits
        let pa_size = match pa_range {
            0 => 32,
            1 => 36,
            2 => 40,
            3 => 42,
            4 => 44,
            5 => 48,
            6 => 52,
            _ => return Err(Error::TranslateVirtualAddressPaNotSupported),
        };

        let indexmask_grainsize = (!0u64) >> (64 - (stride + 3));
        let mut indexmask = (!0u64) >> (64 - (va_size - (stride * (4 - level))));
        // If FEAT_LPA2 is present, the translation table descriptor holds
        // 50 bits of the table address of next level.
        // Otherwise, it is 48 bits.
        let descaddrmask = if ds == 1 {
            !0u64 >> (64 - 50) // mask with 50 least significant bits
        } else {
            !0u64 >> (64 - 48) // mask with 48 least significant bits
        };
        let descaddrmask = descaddrmask & !indexmask_grainsize;

        // Translation table base address
        let mut descaddr: u64 = extract_bits_64!(ttbr1_el1, 0, 48);
        // In the case of FEAT_LPA and FEAT_LPA2, the initial translation table
        // address bits [48:51] comes from TTBR1_EL1 bits [2:5].
        if pa_size == 52 {
            descaddr |= extract_bits_64!(ttbr1_el1, 2, 4) << 48;
        }

        // Loop through tables of each level
        loop {
            // Table offset for current level
            let table_offset: u64 = (gva >> (stride * (4 - level))) & indexmask;
            descaddr |= table_offset;
            descaddr &= !7u64;

            let descriptor: u64 = self
                .guest_mem
                .read_obj_fast(GuestAddress(descaddr))
                .map_err(|_| Error::TranslateVirtualAddress)?;

            descaddr = descriptor & descaddrmask;
            // In the case of FEAT_LPA, the next-level translation table address
            // bits [48:51] comes from bits [12:15] of the current descriptor.
            // For FEAT_LPA2, the next-level translation table address
            // bits [50:51] comes from bits [8:9] of the current descriptor,
            // bits [48:49] comes from bits [48:49] of the descriptor which was
            // handled previously.
            if pa_size == 52 {
                if ds == 1 {
                    // FEAT_LPA2
                    descaddr |= extract_bits_64!(descriptor, 8, 2) << 50;
                } else {
                    // FEAT_LPA
                    descaddr |= extract_bits_64!(descriptor, 12, 4) << 48;
                }
            }

            if (descriptor & 2) != 0 && (level < 3) {
                // This is a table entry. Go down to next level.
                level += 1;
                indexmask = indexmask_grainsize;
                continue;
            }

            break;
        }

        // We have reached either:
        // - a page entry at level 3 or
        // - a block entry at level 1 or 2
        let page_size = 1u64 << ((stride * (4 - level)) + 3);
        descaddr &= !(page_size - 1);
        descaddr |= gva & (page_size - 1);

        Ok(GuestAddress(descaddr))
    }

    pub fn dump_debug(&self, csmap_path: Option<&str>) -> anyhow::Result<String> {
        let mut buf = String::new();

        writeln!(buf, "------ vCPU {} ------", self.id())?;
        writeln!(
            buf,
            // spaces for alignment with TPIDR_EL1
            "PC: 0x{:016x}      LR: 0x{:016x}  FP: 0x{:016x}",
            self.read_raw_reg(hv_reg_t_HV_REG_PC)?,
            self.read_raw_reg(hv_reg_t_HV_REG_LR)?,
            self.read_raw_reg(hv_reg_t_HV_REG_FP)?
        )?;
        let cpsr_mode = self.read_raw_reg(hv_reg_t_HV_REG_CPSR)? & PSR_MODE_MASK;
        let el_str = match cpsr_mode {
            PSR_MODE_EL0T => "EL0t",
            PSR_MODE_EL1T => "EL1t",
            PSR_MODE_EL1H => "EL1h",
            PSR_MODE_EL2T => "EL2t",
            PSR_MODE_EL2H => "EL2h",
            _ => "unknown",
        };
        writeln!(
            buf,
            "SP_EL1: 0x{:016x}  TPIDR_EL1: 0x{:016x}  VBAR_EL1: 0x{:016x}",
            self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_SP_EL1)?,
            self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_TPIDR_EL1)?,
            self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_VBAR_EL1)?,
        )?;
        writeln!(buf, "PSTATE(el): {}", el_str)?;
        writeln!(buf)?;

        match cpsr_mode {
            PSR_MODE_EL1T | PSR_MODE_EL1H => {
                writeln!(buf, "Registers:")?;
                // group 3 regs per line
                for i in 0..32 {
                    write!(
                        buf,
                        "x{:<2}: 0x{:016x}  ",
                        i,
                        self.read_raw_reg(hv_reg_t_HV_REG_X0 + i)?
                    )?;
                    if (i + 1) % 3 == 0 {
                        writeln!(buf)?;
                    }
                }
                // terminate last reg
                writeln!(buf)?;
                // blank line
                writeln!(buf)?;

                writeln!(buf, "Stack:")?;
                if let Some(csmap_path) = csmap_path {
                    if let Err(e) = self.add_debug_stack(&mut buf, csmap_path) {
                        writeln!(buf, "<failed to dump stack: {}>", e)?;
                    }
                } else {
                    writeln!(buf, "<no stack: no System.map>")?;
                }
            }
            _ => {
                writeln!(buf, "<no stack: not in EL1>")?;
            }
        }

        Ok(buf)
    }

    fn walk_stack(&self, pc: u64, mut f: impl FnMut(u64)) -> anyhow::Result<()> {
        let lr = self.read_raw_reg(hv_reg_t_HV_REG_LR)?;

        // start with just PC and LR
        f(pc);

        // on IRQ / exception vector entry, the CPU sets SP and PC but not LR
        // this leads to stack traces with PC=`vectors` and LR=(userspace LR)
        if lr & VA48_MASK == VA48_MASK {
            // subtract 1 so lookup lands on branch instruction
            f(lr - 1);
        }

        // then start looking at FP
        let mut fp = self.read_raw_reg(hv_reg_t_HV_REG_FP)?;
        for i in 0..STACK_DEPTH_LIMIT {
            if fp == 0 {
                // reached end of stack
                break;
            }
            // kernel addresses must have top bits set
            if fp & VA48_MASK != VA48_MASK {
                break;
            }

            // mem[FP+8] = frame's LR
            let frame_lr: u64 = self.guest_mem.read_obj_fast(self.translate_gva(fp + 8)?)?;
            if frame_lr == 0 {
                // reached end of stack
                break;
            }
            // kernel addresses must have top bits set
            if frame_lr & VA48_MASK != VA48_MASK {
                break;
            }

            if i == 0 && frame_lr == lr {
                // skip duplicate LR if FP was already updated (i.e. not in prologue or epilogue)
            } else {
                // subtract 1 so lookup lands on branch instruction
                f(frame_lr - 1);
            }

            // mem[FP] = link to last FP
            fp = self.guest_mem.read_obj_fast(self.translate_gva(fp)?)?;
        }

        Ok(())
    }

    pub fn finish_profiler_sample(
        &self,
        profiler_state: &mut VcpuProfilerState,
        mut sample: PartialSample,
    ) -> anyhow::Result<()> {
        let sample_start = MachAbsoluteTime::now();

        let cpsr = self.read_raw_reg(hv_reg_t_HV_REG_CPSR)?;
        match cpsr & PSR_MODE_MASK {
            PSR_MODE_EL1T | PSR_MODE_EL1H => {
                let pc = self.read_raw_reg(hv_reg_t_HV_REG_PC)?;

                // needs to be in reverse order
                let mut stack = SmallVec::<[u64; STACK_DEPTH_LIMIT]>::new();
                self.walk_stack(pc, |addr| stack.push(addr))?;

                for &addr in stack.iter().rev() {
                    sample.prepend_stack(Frame::new(FrameCategory::GuestKernel, addr));
                }

                // if PC would be returning from a hypercall (PC-4 = HVC), we're in host kernel overhead
                // need to be careful when reading this because of BPF vmalloc_exec regions
                if let Ok(gpa) = self.translate_gva(pc - ARM64_INSN_SIZE) {
                    if let Ok(last_insn) = self.guest_mem.read_obj_fast::<u32>(gpa) {
                        if is_hypercall_insn(last_insn) {
                            sample.prepend_stack(Frame::new(
                                FrameCategory::HostKernel,
                                HostKernelSymbolicator::ADDR_HANDLE_HVC,
                            ));
                        }
                    }
                }
            }
            PSR_MODE_EL0T => {
                sample.prepend_stack(Frame::new(FrameCategory::GuestUserspace, 0));
            }
            _ => {}
        }

        let sample_timestamp = sample.timestamp();
        profiler_state.profiler.queue_sample(sample)?;

        let sample_end = MachAbsoluteTime::now();
        profiler_state
            .histograms
            .sample_time
            .record((sample_end - sample_start).nanos())?;
        profiler_state
            .histograms
            .resume_and_sample
            .record((sample_end - sample_timestamp).nanos())?;

        Ok(())
    }

    pub fn new_symbolicator(&self, csmap_path: &str) -> anyhow::Result<LinuxSymbolicator> {
        // load compact System.map from file system
        // we can't find KASLR offset without symbols, and addrs are useless without KASLR offset
        let csmap = CompactSystemMap::from_slice(&std::fs::read(csmap_path)?)?;

        // find KASLR offset from exception table:
        // VBAR_EL1 = &vectors
        let vbar_el1 = self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_VBAR_EL1)?;
        // calculate KASLR offset
        let kaslr_offset = if vbar_el1 == 0 {
            // early boot; vbar_el1 hasn't been set yet, and neither has KASLR
            // this should instead be the offset from PA to image VA
            let text_addr = csmap
                .symbol_to_vaddr("_text")
                .ok_or_else(|| anyhow!("symbol '_text' not found in System.map"))?;

            (text_addr - DRAM_MEM_START) as i64
        } else {
            // find "vectors"
            let vectors_addr = csmap
                .symbol_to_vaddr("vectors")
                .ok_or_else(|| anyhow!("symbol 'vectors' not found in System.map"))?;

            // KASLR offset is subtracted
            -((vbar_el1 - vectors_addr) as i64)
        };

        LinuxSymbolicator::new(csmap, csmap_path, kaslr_offset)
    }

    fn add_debug_stack(&self, buf: &mut String, csmap_path: &str) -> anyhow::Result<()> {
        // walk stack, with depth limit to protect against malicious/corrupted stack
        let pc = self.read_raw_reg(hv_reg_t_HV_REG_PC)?;
        let mut stack = Vec::with_capacity(STACK_DEPTH_LIMIT);
        self.walk_stack(pc, |addr| stack.push(addr))?;

        let mut symbolicator = self.new_symbolicator(csmap_path)?;
        for addr in stack {
            let symbols = symbolicator.addr_to_symbols(addr)?;
            if symbols.is_empty() {
                writeln!(buf, "  UNKNOWN+0x{:016x}", addr)?;
            } else {
                for sym in symbols {
                    match sym.function {
                        Some(SymbolFunc::Function(symbol, offset)) => {
                            writeln!(buf, "  {}+{}", symbol, offset)?;
                        }
                        Some(SymbolFunc::Inlined(symbol)) => {
                            writeln!(buf, "  [inl] {}", symbol)?;
                        }
                        None => {
                            writeln!(buf, "  UNKNOWN+0x{:016x}", addr)?;
                        }
                    }
                }
            }
        }

        Ok(())
    }

    pub fn destroy(self) {
        let err = unsafe { hv_vcpu_destroy(self.hv_vcpu.0) };
        if err != 0 {
            error!("Failed to destroy vcpu: {err}");
        }
    }

    pub fn request_exit(hv_vcpu: HvVcpuRef) -> Result<(), Error> {
        let mut vcpu: hv_vcpu_t = hv_vcpu.0;
        let ret = unsafe { hv_vcpus_exit(&mut vcpu, 1) };
        HvfError::result(ret).map_err(Error::VcpuRequestExit)
    }

    pub fn set_pending_irq(
        hv_vcpu: HvVcpuRef,
        type_: InterruptType,
        pending: bool,
    ) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_set_pending_interrupt(hv_vcpu.0, type_ as u32, pending) };
        HvfError::result(ret).map_err(Error::VcpuSetPendingIrq)
    }

    pub fn set_vtimer_mask(hv_vcpu: HvVcpuRef, masked: bool) -> Result<(), Error> {
        let ret = unsafe { hv_vcpu_set_vtimer_mask(hv_vcpu.0, masked) };
        HvfError::result(ret).map_err(Error::VcpuSetVtimerMask)
    }
}

// must be 8-byte aligned, so go in units of 8 bytes
unsafe fn search_8b_linear(
    haystack_ptr: *mut u64,
    needle: u64,
    haystack_bytes: usize,
) -> Option<usize> {
    let mut i = 0;
    while i < (haystack_bytes / 8) {
        if unsafe { *haystack_ptr.add(i) } == needle {
            return Some(i);
        }
        i += 1;
    }
    None
}
