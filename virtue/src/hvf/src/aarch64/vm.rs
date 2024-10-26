use std::ffi::c_void;

use arch::aarch64::gic::GICDevice;
use arch::aarch64::{layout, DAX_SIZE};
use arch::ArchMemoryInfo;
use bitflags::bitflags;
use vm_memory::{Address, GuestAddress};

use tracing::{debug, error};

use crate::aarch64::bindings::hv_vm_create;
use crate::aarch64::hvf_gic::GicConfig;
use crate::aarch64::vm_config::VmConfig;
use crate::aarch64::weak_link::OPTIONAL15;
use crate::{call_optional, Error, HvfError};

use super::bindings::{
    hv_memory_flags_t, hv_vcpu_t, hv_vcpus_exit, hv_vm_config_get_default_ipa_size,
    hv_vm_config_get_max_ipa_size, hv_vm_destroy, HV_MEMORY_EXEC, HV_MEMORY_READ, HV_MEMORY_WRITE,
};
use super::hvf_gic::{FdtGic, GicProps};
use super::polyfill;

// macOS 15 knobs
pub const USE_HVF_GIC: bool = false;
pub const ENABLE_NESTED_VIRT: bool = false;

#[derive(Debug, Copy, Clone)]
pub enum ParkError {
    CanNoLongerPark,
}

#[derive(Debug)]
pub struct HvfVm {
    pub gic_props: Option<GicProps>,
}

bitflags! {
    pub struct MemoryFlags: hv_memory_flags_t {
        const READ = HV_MEMORY_READ as hv_memory_flags_t;
        const WRITE = HV_MEMORY_WRITE as hv_memory_flags_t;
        const EXEC = HV_MEMORY_EXEC as hv_memory_flags_t;

        const NONE = 0;
        const RWX = Self::READ.bits() | Self::WRITE.bits() | Self::EXEC.bits();
    }
}

impl HvfVm {
    pub fn new(mem_info: &ArchMemoryInfo, vcpu_count: u8) -> Result<Self, Error> {
        let config = VmConfig::new();

        // how many IPA bits do we need? check highest guest mem address
        let ipa_bits = (mem_info.last_addr_excl().raw_value() - 1).ilog2() + 1;
        debug!("IPA size: {} bits", ipa_bits);
        if ipa_bits > Self::get_default_ipa_size()? {
            // if we need more than default, make sure HW supports it
            if ipa_bits > Self::get_max_ipa_size()? {
                return Err(Error::VmConfigIpaSizeLimit(ipa_bits));
            }

            // it's supported. set it
            config.set_ipa_size(ipa_bits)?;
        }

        // we use this to prevent an invalid debug feature combination
        #[allow(clippy::assertions_on_constants)]
        if ENABLE_NESTED_VIRT {
            // our GIC impl doesn't support EL2
            // we'd also need a custom HVC interface to set ICH regs for injection
            assert!(USE_HVF_GIC);
            config.set_el2_enabled(true)?;
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

        let ret = unsafe { hv_vm_create(config.as_ptr()) };
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
        let ret = unsafe { call_optional!(OPTIONAL15.hv_gic_set_spi(irq, true)) };
        HvfError::result(ret).map_err(Error::GicAssertSpi)
    }

    /// # Safety
    /// host_start_addr must be a mapped, contiguous host memory region of at least `size` bytes
    pub unsafe fn map_memory(
        &self,
        host_start_addr: *mut u8,
        guest_start_addr: GuestAddress,
        size: usize,
        flags: MemoryFlags,
    ) -> Result<(), Error> {
        let ret = unsafe {
            polyfill::vm_map(
                host_start_addr as *mut c_void,
                guest_start_addr.raw_value(),
                size,
                flags.bits(),
            )
        };
        HvfError::result(ret).map_err(Error::MemoryMap)
    }

    pub fn unmap_memory(&self, guest_start_addr: GuestAddress, size: usize) -> Result<(), Error> {
        let ret = unsafe { polyfill::vm_unmap(guest_start_addr.raw_value(), size) };
        HvfError::result(ret).map_err(Error::MemoryUnmap)
    }

    pub fn protect_memory(
        &self,
        guest_start_addr: GuestAddress,
        size: usize,
        flags: MemoryFlags,
    ) -> Result<(), Error> {
        let ret = unsafe { polyfill::vm_protect(guest_start_addr.raw_value(), size, flags.bits()) };
        HvfError::result(ret).map_err(Error::MemoryProtect)
    }

    pub fn force_exits(&self, vcpu_ids: &[hv_vcpu_t]) -> Result<(), Error> {
        // HVF won't mutate this
        let ret = unsafe { hv_vcpus_exit(vcpu_ids.as_ptr() as *mut _, vcpu_ids.len() as u32) };
        HvfError::result(ret).map_err(Error::VcpuRequestExit)
    }

    pub fn destroy(&self) {
        let ret = unsafe { hv_vm_destroy() };
        if let Err(e) = HvfError::result(ret) {
            error!("Failed to destroy VM: {:?}", e);
        }
    }

    fn get_default_ipa_size() -> Result<u32, Error> {
        let mut ipa_bit_length: u32 = 0;
        let ret = unsafe { hv_vm_config_get_default_ipa_size(&mut ipa_bit_length) };
        HvfError::result(ret).map_err(Error::VmConfigGetDefaultIpaSize)?;
        Ok(ipa_bit_length)
    }

    fn get_max_ipa_size() -> Result<u32, Error> {
        let mut ipa_bit_length: u32 = 0;
        let ret = unsafe { hv_vm_config_get_max_ipa_size(&mut ipa_bit_length) };
        HvfError::result(ret).map_err(Error::VmConfigGetMaxIpaSize)?;
        Ok(ipa_bit_length)
    }

    pub fn max_ram_size() -> Result<u64, Error> {
        let max_addr = (1 << Self::get_max_ipa_size()?) - 1;
        let max_ram_addr = max_addr - DAX_SIZE - 0x4000_0000; // shm rounding (ceil) = 1 GiB
        Ok(max_ram_addr - layout::DRAM_MEM_START)
    }
}
