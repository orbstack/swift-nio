use arch::aarch64::gic::{self, GICDevice};

use crate::aarch64::bindings::hv_gic_intid_t_HV_GIC_INT_MAINTENANCE;
use crate::aarch64::weak_link::OPTIONAL15;
use crate::call_optional;

use super::bindings::hv_gic_config_t;
use super::vm::USE_HVF_GIC;
use super::{Error, HvfError};

#[derive(Debug, Clone)]
pub struct GicProps {
    pub dist_base: u64,
    pub dist_size: u64,
    pub redist_base: u64,
    pub redist_total_size: u64,
    pub vcpu_count: u64,
}

pub struct FdtGic {
    props: GicProps,
    regs: Vec<u64>,
}

impl FdtGic {
    pub fn from_props(props: GicProps) -> Self {
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
        let ret = unsafe {
            call_optional!(
                OPTIONAL15.hv_gic_get_intid(hv_gic_intid_t_HV_GIC_INT_MAINTENANCE, &mut irq)
            )
        };
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

pub struct GicConfig {
    ptr: hv_gic_config_t,
}

impl GicConfig {
    pub fn new() -> Option<Self> {
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

    pub fn get_distributor_size() -> Result<usize, Error> {
        let mut dist_size: usize = 0;
        let ret = unsafe { call_optional!(OPTIONAL15.hv_gic_get_distributor_size(&mut dist_size)) };
        HvfError::result(ret).map_err(Error::GicGetDistributorSize)?;
        Ok(dist_size)
    }

    pub fn get_redistributor_region_size() -> Result<usize, Error> {
        let mut redist_total_size: usize = 0;
        let ret = unsafe {
            call_optional!(OPTIONAL15.hv_gic_get_redistributor_region_size(&mut redist_total_size))
        };
        HvfError::result(ret).map_err(Error::GicGetRedistributorSize)?;
        Ok(redist_total_size)
    }

    pub fn set_distributor_base(&self, dist_base: u64) -> Result<(), Error> {
        let ret = unsafe {
            call_optional!(OPTIONAL15.hv_gic_config_set_distributor_base(self.ptr, dist_base))
        };
        HvfError::result(ret).map_err(Error::GicConfigSetDistributorBase)
    }

    pub fn set_redistributor_base(&self, redist_base: u64) -> Result<(), Error> {
        let ret = unsafe {
            call_optional!(OPTIONAL15.hv_gic_config_set_redistributor_base(self.ptr, redist_base))
        };
        HvfError::result(ret).map_err(Error::GicConfigSetRedistributorBase)
    }

    pub fn create_gic(&self) -> Result<(), Error> {
        let ret = unsafe { call_optional!(OPTIONAL15.hv_gic_create(self.ptr)) };
        HvfError::result(ret).map_err(Error::GicCreate)
    }
}
