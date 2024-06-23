// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

#[allow(non_camel_case_types)]
#[allow(improper_ctypes)]
#[allow(dead_code)]
#[allow(non_snake_case)]
#[allow(non_upper_case_globals)]
#[allow(deref_nullptr)]
mod bindings;
use arch::aarch64::gic::{self, GICDevice};
use arch::aarch64::{layout, MMIO_SHM_SIZE};
use bindings::*;
use bitflags::bitflags;
use dlopen_derive::WrapperApi;
use gruel::{StartupAbortedError, StartupTask};
use once_cell::sync::Lazy;
use vm_memory::{Address, ByteValued, GuestAddress, GuestMemory, GuestMemoryMmap, VolatileMemory};

use dlopen::wrapper::{Container, WrapperApi};
use vmm_ids::{ArcVcpuSignal, VcpuSignal};

use std::arch::asm;
use std::convert::TryInto;
use std::ffi::c_void;
use std::mem::size_of;
use std::sync::atomic::{AtomicIsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

use crossbeam_channel::Sender;
use num_derive::FromPrimitive;
use num_traits::FromPrimitive;
use tracing::{debug, error};

use counter::RateCounter;

use crate::hypercalls::{
    PSCI_CPU_ON, PSCI_MIGRATE_TYPE, PSCI_POWER_OFF, PSCI_RESET, PSCI_VERSION, RSVM_FEATURES,
    RSVM_IO_REQ, RSVM_PVGIC_SET_ADDR, RSVM_SET_ACTLR_EL1, VZF_PVLOCK_KICK, VZF_PVLOCK_WAIT,
};

extern "C" {
    pub fn mach_absolute_time() -> u64;
}

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
    COUNT_EXIT_WFE_EXPIRED in "hvf.vmexit.wfe.expired": RateCounter = RateCounter::new(FILTER);
    COUNT_EXIT_VTIMER in "hvf.vmexit.vtimer": RateCounter = RateCounter::new(FILTER);
}

// macOS 15 knobs
const USE_HVF_GIC: bool = false;
const ENABLE_NESTED_VIRT: bool = false;

const HV_EXIT_REASON_CANCELED: hv_exit_reason_t = 0;
const HV_EXIT_REASON_EXCEPTION: hv_exit_reason_t = 1;
const HV_EXIT_REASON_VTIMER_ACTIVATED: hv_exit_reason_t = 2;

const PSR_MODE_EL1H: u64 = 0x0000_0005;
const PSR_MODE_EL2H: u64 = 0x0000_0009;
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

// macOS 12+ APIs
static OPTIONAL12: Lazy<Option<Container<HvfOptional12>>> =
    Lazy::new(|| unsafe { Container::load_self() }.ok());

// macOS 15+ APIs
static OPTIONAL15: Lazy<Option<Container<HvfOptional15>>> =
    Lazy::new(|| unsafe { Container::load_self() }.ok());

#[derive(thiserror::Error, Debug, FromPrimitive)]
#[repr(i32)]
pub enum HvfError {
    #[error("error")]
    Error = HV_ERROR,
    #[error("busy")]
    Busy = HV_BUSY,
    #[error("bad argument")]
    BadArgument = HV_BAD_ARGUMENT,
    #[error("illegal guest state")]
    IllegalGuestState = HV_ILLEGAL_GUEST_STATE,
    #[error("no resources")]
    NoResources = HV_NO_RESOURCES,
    #[error("no device")]
    NoDevice = HV_NO_DEVICE,
    #[error("denied")]
    Denied = HV_DENIED,
    #[error("unsupported")]
    Unsupported = HV_UNSUPPORTED,
    #[error("unknown")]
    Unknown = -1,
}

impl HvfError {
    fn result(ret: hv_return_t) -> Result<(), Self> {
        match ret {
            HV_SUCCESS => Ok(()),
            _ => Err(HvfError::from_i32(ret).unwrap_or(HvfError::Unknown)),
        }
    }
}

#[derive(thiserror::Error, Debug)]
pub enum Error {
    #[error("memory map: {0}")]
    MemoryMap(HvfError),
    #[error("memory unmap: {0}")]
    MemoryUnmap(HvfError),
    #[error("vcpu create: {0}")]
    VcpuCreate(HvfError),
    #[error("vcpu initial registers: {0}")]
    VcpuInitialRegisters(HvfError),
    #[error("vcpu read register: {0}")]
    VcpuReadRegister(HvfError),
    #[error("vcpu read system register: {0}")]
    VcpuReadSystemRegister(HvfError),
    #[error("vcpu request exit: {0}")]
    VcpuRequestExit(HvfError),
    #[error("vcpu run: {0}")]
    VcpuRun(HvfError),
    #[error("vcpu set pending irq: {0}")]
    VcpuSetPendingIrq(HvfError),
    #[error("vcpu set register: {0}")]
    VcpuSetRegister(HvfError),
    #[error("vcpu set system register: {0}")]
    VcpuSetSystemRegister(HvfError),
    #[error("vcpu set vtimer mask: {0}")]
    VcpuSetVtimerMask(HvfError),
    #[error("vm config set ipa size: {0}")]
    VmConfigSetIpaSize(HvfError),
    #[error("vm config enable nested virt: {0}")]
    VmConfigEnableNestedVirt(HvfError),
    #[error("vm create: {0}")]
    VmCreate(HvfError),
    #[error("vm allocate: {0}")]
    VmAllocate(HvfError),
    #[error("vm deallocate: {0}")]
    VmDeallocate(HvfError),
    #[error("host CPU doesn't support assigning {0} bits of VM memory")]
    VmConfigIpaSizeLimit(u32),
    #[error("vm config get default ipa size: {0}")]
    VmConfigGetDefaultIpaSize(HvfError),
    #[error("vm config get max ipa size: {0}")]
    VmConfigGetMaxIpaSize(HvfError),
    #[error("guest memory map")]
    GetGuestMemory,
    #[error("vm config get el2 supported: {0}")]
    VmConfigGetEl2Supported(HvfError),
    #[error("vm config set el2 enabled: {0}")]
    VmConfigSetEl2Enabled(HvfError),
    #[error("gic config create")]
    GicConfigCreate,
    #[error("gic get distributor size: {0}")]
    GicGetDistributorSize(HvfError),
    #[error("gic get redistributor size: {0}")]
    GicGetRedistributorSize(HvfError),
    #[error("gic config set distributor base: {0}")]
    GicConfigSetDistributorBase(HvfError),
    #[error("gic config set redistributor base: {0}")]
    GicConfigSetRedistributorBase(HvfError),
    #[error("gic create: {0}")]
    GicCreate(HvfError),
    #[error("gic get intid: {0}")]
    GicGetIntid(HvfError),
    #[error("gic set intid: {0}")]
    GicAssertSpi(HvfError),
    #[error("gic get spi range: {0}")]
    GicGetSpiRange(HvfError),
    #[error("gic config set msi size: {0}")]
    GicConfigSetMsiSize(HvfError),
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

pub fn vcpu_request_exit(hv_vcpu: HvVcpuRef) -> Result<(), Error> {
    let mut vcpu: u64 = hv_vcpu.0;
    let ret = unsafe { hv_vcpus_exit(&mut vcpu, 1) };
    HvfError::result(ret).map_err(Error::VcpuRequestExit)
}

pub fn vcpu_set_pending_irq(
    hv_vcpu: HvVcpuRef,
    irq_type: InterruptType,
    pending: bool,
) -> Result<(), Error> {
    let _type = match irq_type {
        InterruptType::Irq => hv_interrupt_type_t_HV_INTERRUPT_TYPE_IRQ,
        InterruptType::Fiq => hv_interrupt_type_t_HV_INTERRUPT_TYPE_FIQ,
    };

    let ret = unsafe { hv_vcpu_set_pending_interrupt(hv_vcpu.0, _type, pending) };
    HvfError::result(ret).map_err(Error::VcpuSetPendingIrq)
}

pub fn vcpu_set_vtimer_mask(hv_vcpu: HvVcpuRef, masked: bool) -> Result<(), Error> {
    let ret = unsafe { hv_vcpu_set_vtimer_mask(hv_vcpu.0, masked) };
    HvfError::result(ret).map_err(Error::VcpuSetVtimerMask)
}

pub type VcpuId = u64;

pub trait Parkable: Send + Sync {
    fn park(&self) -> Result<StartupTask, StartupAbortedError>;

    fn unpark(&self, unpark_task: StartupTask);

    fn register_vcpu(&self, vcpu: ArcVcpuSignal) -> StartupTask;

    fn process_park_commands(
        &self,
        signal: &VcpuSignal,
        park_task: StartupTask,
    ) -> Result<StartupTask, StartupAbortedError>;
}

#[derive(WrapperApi)]
struct HvfOptional12 {
    hv_vm_config_create: unsafe extern "C" fn() -> hv_vm_config_t,
    hv_vm_config_get_max_ipa_size: unsafe extern "C" fn(ipa_bit_length: *mut u32) -> hv_return_t,
    hv_vm_config_get_default_ipa_size:
        unsafe extern "C" fn(ipa_bit_length: *mut u32) -> hv_return_t,
    hv_vm_config_set_ipa_size:
        unsafe extern "C" fn(config: hv_vm_config_t, ipa_bit_length: u32) -> hv_return_t,
}

#[derive(WrapperApi)]
struct HvfOptional15 {
    hv_gic_config_create: unsafe extern "C" fn() -> hv_gic_config_t,
    hv_gic_get_distributor_size: unsafe extern "C" fn(distributor_size: *mut usize) -> hv_return_t,
    hv_gic_get_redistributor_region_size:
        unsafe extern "C" fn(redistributor_region_size: *mut usize) -> hv_return_t,
    hv_gic_config_set_distributor_base: unsafe extern "C" fn(
        config: hv_gic_config_t,
        distributor_base_address: hv_ipa_t,
    ) -> hv_return_t,
    hv_gic_config_set_redistributor_base: unsafe extern "C" fn(
        config: hv_gic_config_t,
        redistributor_base_address: hv_ipa_t,
    ) -> hv_return_t,

    hv_vm_config_set_el2_enabled:
        unsafe extern "C" fn(config: hv_vm_config_t, el2_enabled: bool) -> hv_return_t,
}

#[derive(Debug, Copy, Clone)]
pub enum ParkError {
    CanNoLongerPark,
}

#[derive(Clone, Debug)]
pub struct HvfVm {
    pub gic_props: Option<GicProps>,
}

#[derive(Debug, Clone)]
pub struct GicProps {
    pub dist_base: u64,
    pub dist_size: u64,
    pub redist_base: u64,
    pub redist_total_size: u64,
    pub vcpu_count: u64,
}

struct FdtGic {
    props: GicProps,
    regs: Vec<u64>,
}

impl FdtGic {
    fn from_props(props: GicProps) -> Self {
        let regs = vec![
            props.dist_base,
            props.dist_size,
            props.redist_base,
            props.redist_total_size,
        ];

        Self { props, regs }
    }
}

impl GICDevice for FdtGic {
    fn device_properties(&self) -> &[u64] {
        &self.regs
    }

    fn vcpu_count(&self) -> u64 {
        self.props.vcpu_count
    }

    fn fdt_compatibility(&self) -> &str {
        "arm,gic-v3"
    }

    fn fdt_maint_irq(&self) -> u32 {
        let mut irq = 0;
        let ret = unsafe { hv_gic_get_intid(hv_gic_intid_t_HV_GIC_INT_MAINTENANCE, &mut irq) };
        HvfError::result(ret).map_err(Error::GicGetIntid).unwrap();
        irq
    }

    fn version() -> u32 {
        3
    }

    fn create_device(_vcpu_count: u64) -> Box<dyn GICDevice> {
        unimplemented!();
    }

    fn init_device_attributes(_gic_device: &Box<dyn GICDevice>) -> gic::Result<()> {
        unimplemented!();
    }
}

macro_rules! call_optional {
    ($optional: expr, $method: ident, $($args: expr),*) => {
        $optional.as_ref().unwrap().$method($($args),*)
    };
}

struct VmConfig {
    ptr: hv_vm_config_t,
}

impl VmConfig {
    fn new() -> Option<Self> {
        if let Some(hvf_optional) = OPTIONAL12.as_ref() {
            let ptr = unsafe { hvf_optional.hv_vm_config_create() };
            Some(Self { ptr })
        } else {
            None
        }
    }

    fn ptr(&self) -> hv_vm_config_t {
        self.ptr
    }

    fn set_ipa_size(&self, ipa_bits: u32) -> Result<(), Error> {
        let ret =
            unsafe { call_optional!(OPTIONAL12, hv_vm_config_set_ipa_size, self.ptr, ipa_bits) };
        HvfError::result(ret).map_err(Error::VmConfigSetIpaSize)
    }

    fn set_el2_enabled(&self, enabled: bool) -> Result<(), Error> {
        let ret =
            unsafe { call_optional!(OPTIONAL15, hv_vm_config_set_el2_enabled, self.ptr, enabled) };
        HvfError::result(ret).map_err(Error::VmConfigSetEl2Enabled)
    }
}

impl Drop for VmConfig {
    fn drop(&mut self) {
        unsafe { os_release(self.ptr as *mut c_void) };
    }
}

struct GicConfig {
    ptr: hv_gic_config_t,
}

impl GicConfig {
    fn new() -> Option<Self> {
        if !USE_HVF_GIC {
            return None;
        }

        if let Some(hvf_optional) = OPTIONAL15.as_ref() {
            let ptr = unsafe { hvf_optional.hv_gic_config_create() };
            Some(Self { ptr })
        } else {
            // TODO: fail with None when macOS 15 is stable
            // this is a loud failure for testing
            panic!("GIC API not available");
        }
    }

    fn get_distributor_size() -> Result<usize, Error> {
        let mut dist_size: usize = 0;
        let ret =
            unsafe { call_optional!(OPTIONAL15, hv_gic_get_distributor_size, &mut dist_size) };
        HvfError::result(ret).map_err(Error::GicGetDistributorSize)?;
        Ok(dist_size)
    }

    fn get_redistributor_region_size() -> Result<usize, Error> {
        let mut redist_total_size: usize = 0;
        let ret = unsafe {
            call_optional!(
                OPTIONAL15,
                hv_gic_get_redistributor_region_size,
                &mut redist_total_size
            )
        };
        HvfError::result(ret).map_err(Error::GicGetRedistributorSize)?;
        Ok(redist_total_size)
    }

    fn set_distributor_base(&self, dist_base: u64) -> Result<(), Error> {
        let ret = unsafe {
            call_optional!(
                OPTIONAL15,
                hv_gic_config_set_distributor_base,
                self.ptr,
                dist_base
            )
        };
        HvfError::result(ret).map_err(Error::GicConfigSetDistributorBase)
    }

    fn set_redistributor_base(&self, redist_base: u64) -> Result<(), Error> {
        let ret = unsafe {
            call_optional!(
                OPTIONAL15,
                hv_gic_config_set_redistributor_base,
                self.ptr,
                redist_base
            )
        };
        HvfError::result(ret).map_err(Error::GicConfigSetRedistributorBase)
    }

    fn create_gic(&self) -> Result<(), Error> {
        let ret = unsafe { hv_gic_create(self.ptr) };
        HvfError::result(ret).map_err(Error::GicCreate)
    }
}

impl HvfVm {
    pub fn new(guest_mem: &GuestMemoryMmap, vcpu_count: u8) -> Result<Self, Error> {
        let config = VmConfig::new();

        // how many IPA bits do we need? check highest guest mem address
        let ipa_bits = guest_mem.last_addr().raw_value().ilog2() + 1;
        debug!("IPA size: {} bits", ipa_bits);
        if ipa_bits > Self::get_default_ipa_size()? {
            // if we need more than default, make sure HW supports it
            if ipa_bits > Self::get_max_ipa_size()? {
                return Err(Error::VmConfigIpaSizeLimit(ipa_bits));
            }
            let Some(ref config) = config else {
                return Err(Error::VmConfigIpaSizeLimit(ipa_bits));
            };

            // it's supported. set it
            config.set_ipa_size(ipa_bits)?;
        }

        if ENABLE_NESTED_VIRT {
            // our GIC impl doesn't support EL2
            // we'd also need a custom HVC interface to set ICH regs for injection
            assert!(USE_HVF_GIC);
            config.as_ref().unwrap().set_el2_enabled(true)?;
        }

        let gic_config = GicConfig::new();
        let gic_props = if let Some(ref gic_config) = gic_config {
            let dist_size = GicConfig::get_distributor_size()?;
            let redist_total_size = GicConfig::get_redistributor_region_size()?;

            let dist_base = layout::MAPPED_IO_START - dist_size as u64;
            gic_config.set_distributor_base(dist_base)?;

            let redist_base = dist_base - redist_total_size as u64;
            gic_config.set_redistributor_base(redist_base)?;

            Some(GicProps {
                dist_base,
                dist_size: dist_size as u64,
                redist_base,
                redist_total_size: redist_total_size as u64,
                vcpu_count: vcpu_count as u64,
            })
        } else {
            None
        };

        let ret = unsafe {
            hv_vm_create(
                config
                    .as_ref()
                    .map(|c| c.ptr())
                    .unwrap_or(std::ptr::null_mut()),
            )
        };
        HvfError::result(ret).map_err(Error::VmCreate)?;

        // GIC must be created after VM
        if let Some(gic_config) = gic_config {
            gic_config.create_gic()?;
        }

        Ok(Self { gic_props })
    }

    pub fn get_fdt_gic(&self) -> Option<Box<dyn GICDevice>> {
        if let Some(gic_props) = self.gic_props.as_ref() {
            Some(Box::new(FdtGic::from_props(gic_props.clone())))
        } else {
            None
        }
    }

    pub fn assert_spi(&self, irq: u32) -> Result<(), Error> {
        let ret = unsafe { hv_gic_set_spi(irq, true) };
        HvfError::result(ret).map_err(Error::GicAssertSpi)
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
        HvfError::result(ret).map_err(Error::MemoryMap)
    }

    pub fn unmap_memory(&self, guest_start_addr: u64, size: u64) -> Result<(), Error> {
        let ret = unsafe { hv_vm_unmap(guest_start_addr, size as usize) };
        HvfError::result(ret).map_err(Error::MemoryUnmap)
    }

    pub fn force_exits(&self, vcpu_ids: &mut Vec<hv_vcpu_t>) -> Result<(), Error> {
        let ret = unsafe { hv_vcpus_exit(vcpu_ids.as_mut_ptr(), vcpu_ids.len() as u32) };
        HvfError::result(ret).map_err(Error::VcpuRequestExit)
    }

    pub fn destroy(&self) {
        let ret = unsafe { hv_vm_destroy() };
        if let Err(e) = HvfError::result(ret) {
            error!("Failed to destroy VM: {:?}", e);
        }
    }

    fn get_default_ipa_size() -> Result<u32, Error> {
        if let Some(hvf_optional) = OPTIONAL12.as_ref() {
            let mut ipa_bit_length: u32 = 0;
            let ret =
                unsafe { hvf_optional.hv_vm_config_get_default_ipa_size(&mut ipa_bit_length) };
            HvfError::result(ret).map_err(Error::VmConfigGetDefaultIpaSize)?;
            Ok(ipa_bit_length)
        } else {
            Ok(36)
        }
    }

    fn get_max_ipa_size() -> Result<u32, Error> {
        if let Some(hvf_optional) = OPTIONAL12.as_ref() {
            let mut ipa_bit_length: u32 = 0;
            let ret = unsafe { hvf_optional.hv_vm_config_get_max_ipa_size(&mut ipa_bit_length) };
            HvfError::result(ret).map_err(Error::VmConfigGetMaxIpaSize)?;
            Ok(ipa_bit_length)
        } else {
            Ok(36)
        }
    }

    pub fn max_ram_size() -> Result<u64, Error> {
        let max_addr = (1 << Self::get_max_ipa_size()?) - 1;
        let max_ram_addr = max_addr - MMIO_SHM_SIZE - 0x4000_0000; // shm rounding (ceil) = 1 GiB
        Ok(max_ram_addr - layout::DRAM_MEM_START)
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

bitflags! {
    #[derive(Debug, Clone, Copy)]
    pub struct PvgicFlags: u32 {
        const PVGIC_FLAG_IAR1_PENDING = 1 << 0;
        const PVGIC_FLAG_IAR1_READ = 1 << 1;
    }

    #[derive(Debug, Clone, Copy)]
    pub struct ExitActions: u32 {
        const READ_IAR1_EL1 = 1 << 0;
    }
}

// no atomics because it's on the same CPU, but must be volatile
#[derive(Debug, Clone, Copy)]
struct PvgicVcpuState {
    flags: PvgicFlags,
    // only guest reads this
    #[allow(dead_code)]
    pending_iar1_read: u32,
}

unsafe impl ByteValued for PvgicVcpuState {}

#[derive(Debug, Clone, Copy)]
pub struct HvVcpuRef(pub hv_vcpu_t);

pub struct HvfVcpu {
    parker: Arc<dyn Parkable>,
    hv_vcpu: HvVcpuRef,
    vcpu_exit_ptr: *mut hv_vcpu_exit_t,
    cntfrq: u64,
    mmio_buf: [u8; 8],
    pending_mmio_read: Option<MmioRead>,
    pending_advance_pc: bool,

    allow_actlr: bool,
    actlr_el1_ptr: *mut u64,

    guest_mem: GuestMemoryMmap,
    pvgic: Option<*mut PvgicVcpuState>,
}

extern "C" {
    pub fn _hv_vcpu_get_context(vcpu: hv_vcpu_t) -> *mut c_void;
}

// must be 8-byte aligned, so go in units of 8 bytes
fn search_8b_linear(haystack_ptr: *mut u64, needle: u64, haystack_bytes: usize) -> Option<usize> {
    let mut i = 0;
    while i < (haystack_bytes / 8) {
        if unsafe { *haystack_ptr.add(i) } == needle {
            return Some(i);
        }
        i += 1;
    }
    None
}

impl HvfVcpu {
    pub fn new(parker: Arc<dyn Parkable>, guest_mem: GuestMemoryMmap) -> Result<Self, Error> {
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
        HvfError::result(ret).map_err(Error::VcpuCreate)?;

        Ok(Self {
            parker,
            hv_vcpu: HvVcpuRef(vcpuid),
            vcpu_exit_ptr,
            cntfrq,
            mmio_buf: [0; 8],
            pending_mmio_read: None,
            pending_advance_pc: false,

            allow_actlr: false,
            actlr_el1_ptr: std::ptr::null_mut(),

            guest_mem,
            pvgic: None,
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
            self.write_actlr_el1(ACTLR_EL1_MYSTERY | ACTLR_EL1_EN_TSO)?;
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

    pub fn read_raw_reg(&self, reg: u32) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_get_reg(self.hv_vcpu.0, reg, &mut val) };
        HvfError::result(ret).map_err(Error::VcpuReadRegister)?;
        Ok(val)
    }

    pub fn write_raw_reg(&mut self, reg: u32, val: u64) -> Result<(), Error> {
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

    fn read_sys_reg(&self, reg: u16) -> Result<u64, Error> {
        let mut val: u64 = 0;
        let ret = unsafe { hv_vcpu_get_sys_reg(self.hv_vcpu.0, reg, &mut val) };
        HvfError::result(ret).map_err(Error::VcpuReadSystemRegister)?;
        Ok(val)
    }

    fn write_sys_reg(&mut self, reg: u16, val: u64) -> Result<(), Error> {
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

            let sctlr_offset = search_8b_linear(vcpu_ptr as *mut u64, SYS_REG_SENTINEL, 4096)
                .ok_or(Error::VcpuInitialRegisters(HvfError::Unknown))?;
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

        if let Some(pending_irq) = pending_irq {
            vcpu_set_pending_irq(self.hv_vcpu, InterruptType::Irq, true)?;

            if let Some(pvgic_ptr) = self.pvgic {
                let pvgic = unsafe { &mut *pvgic_ptr };
                // if there's a pending IRQ, IAR1_EL1 always has a valid value (!= 1023)
                pvgic.flags = PvgicFlags::PVGIC_FLAG_IAR1_PENDING;
                pvgic.pending_iar1_read = pending_irq;
            }
        }

        let ret = unsafe { hv_vcpu_run(self.hv_vcpu.0) };
        HvfError::result(ret).map_err(Error::VcpuRun)?;

        COUNT_EXIT_TOTAL.count();

        if pending_irq.is_some() {
            if let Some(pvgic_ptr) = self.pvgic {
                let pvgic = unsafe { &*pvgic_ptr };
                if pvgic.flags.contains(PvgicFlags::PVGIC_FLAG_IAR1_READ) {
                    // we can only return one vmexit here, so tell the emulation loop to trigger IAR1_EL1 read for side effects (dequeue)
                    // usually this will happen when the guest hits EOIR_EL1 write
                    exit_actions.insert(ExitActions::READ_IAR1_EL1);
                }
            }
        }

        let vcpu_exit = unsafe { &*self.vcpu_exit_ptr };
        let exit = match vcpu_exit.reason {
            HV_EXIT_REASON_CANCELED => VcpuExit::Canceled,
            HV_EXIT_REASON_EXCEPTION => {
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

                            COUNT_EXIT_MMIO_READ.count();
                            VcpuExit::MmioWrite(pa, &self.mmio_buf[0..len])
                        } else {
                            COUNT_EXIT_MMIO_WRITE.count();
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
                            let cval = self.read_sys_reg(hv_sys_reg_t_HV_SYS_REG_CNTV_CVAL_EL0)?;
                            let now = unsafe { mach_absolute_time() };

                            if now > cval {
                                COUNT_EXIT_WFE_EXPIRED.count();
                                VcpuExit::WaitForEventExpired
                            } else {
                                let timeout = Duration::from_nanos(
                                    (cval - now) * (1_000_000_000 / self.cntfrq),
                                );
                                COUNT_EXIT_WFE_TIMED.count();
                                VcpuExit::WaitForEventTimeout(timeout)
                            }
                        }
                    }

                    _ => panic!("unexpected exception: 0x{ec:x}"),
                }
            }

            HV_EXIT_REASON_VTIMER_ACTIVATED => {
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

            RSVM_FEATURES => {
                self.write_raw_reg(hv_reg_t_HV_REG_X0, 0)?;
                return Ok(VcpuExit::HypervisorCall);
            }

            RSVM_IO_REQ => {
                COUNT_EXIT_HVC_VIRTIOFS.count();
                let dev_id = self.read_raw_reg(hv_reg_t_HV_REG_X1)? as usize;
                let args_ptr = self.read_raw_reg(hv_reg_t_HV_REG_X2)? as usize;
                return Ok(VcpuExit::HypervisorIoCall { dev_id, args_ptr });
            }

            RSVM_PVGIC_SET_ADDR => {
                if USE_HVF_GIC {
                    None
                } else {
                    let pvgic_state_addr = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                    let slice = self
                        .guest_mem
                        .get_slice(GuestAddress(pvgic_state_addr), size_of::<PvgicVcpuState>())
                        .map_err(|_| Error::GetGuestMemory)?;
                    let mut_ref =
                        unsafe { slice.aligned_as_mut(0).map_err(|_| Error::GetGuestMemory)? };
                    self.pvgic = Some(mut_ref as *mut PvgicVcpuState);
                    Some(0)
                }
            }

            RSVM_SET_ACTLR_EL1 => {
                COUNT_EXIT_HVC_ACTLR.count();

                if self.allow_actlr {
                    let value = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                    self.write_actlr_el1(value & ACTLR_EL1_ALLOWED_MASK)?;
                }

                return Ok(VcpuExit::HypervisorCall);
            }

            VZF_PVLOCK_WAIT => {
                COUNT_EXIT_HVC_PVLOCK_WAIT.count();
                return Ok(VcpuExit::PvlockPark);
            }

            VZF_PVLOCK_KICK => {
                COUNT_EXIT_HVC_PVLOCK_KICK.count();
                let vcpuid = self.read_raw_reg(hv_reg_t_HV_REG_X1)?;
                return Ok(VcpuExit::PvlockUnpark(vcpuid));
            }

            _ => {
                debug!("HVC call unhandled");
                None
            }
        };

        // SMCCC not supported
        self.write_raw_reg(hv_reg_t_HV_REG_X0, ret.unwrap_or(-1i64 as u64))?;
        Ok(VcpuExit::HypervisorCall)
    }

    pub fn destroy(self) {
        let err = unsafe { hv_vcpu_destroy(self.hv_vcpu.0) };
        if err != 0 {
            error!("Failed to destroy vcpu: {err}");
        }
    }
}

pub unsafe fn vm_allocate(size: usize) -> Result<*mut c_void, Error> {
    let mut ptr: *mut c_void = std::ptr::null_mut();
    let ret = unsafe { hv_vm_allocate(&mut ptr, size, HV_ALLOCATE_DEFAULT as u64) };
    HvfError::result(ret).map_err(Error::VmAllocate)?;
    Ok(ptr)
}

pub unsafe fn vm_deallocate(ptr: *mut c_void, size: usize) -> Result<(), Error> {
    let ret = unsafe { hv_vm_deallocate(ptr, size) };
    HvfError::result(ret).map_err(Error::VmDeallocate)
}

pub fn vcpu_id_to_mpidr(vcpu_id: u64) -> u64 {
    vcpu_id << 8
}
