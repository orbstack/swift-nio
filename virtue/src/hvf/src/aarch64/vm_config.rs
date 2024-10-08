use std::ffi::c_void;

use crate::aarch64::weak_link::OPTIONAL15;
use crate::call_optional;

use super::bindings::{hv_vm_config_create, hv_vm_config_set_ipa_size, hv_vm_config_t, os_release};
use super::{Error, HvfError};

pub struct VmConfig {
    ptr: hv_vm_config_t,
}

impl VmConfig {
    pub fn new() -> Self {
        let ptr = unsafe { hv_vm_config_create() };
        Self { ptr }
    }

    pub fn as_ptr(&self) -> hv_vm_config_t {
        self.ptr
    }

    pub fn set_ipa_size(&self, ipa_bits: u32) -> Result<(), Error> {
        let ret = unsafe { hv_vm_config_set_ipa_size(self.ptr, ipa_bits) };
        HvfError::result(ret).map_err(Error::VmConfigSetIpaSize)
    }

    pub fn set_el2_enabled(&self, enabled: bool) -> Result<(), Error> {
        let ret =
            unsafe { call_optional!(OPTIONAL15.hv_vm_config_set_el2_enabled(self.ptr, enabled)) };
        HvfError::result(ret).map_err(Error::VmConfigSetEl2Enabled)
    }
}

impl Drop for VmConfig {
    fn drop(&mut self) {
        unsafe { os_release(self.ptr as *mut c_void) };
    }
}
