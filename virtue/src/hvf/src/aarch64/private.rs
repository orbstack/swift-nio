use std::ffi::c_void;

use super::bindings::hv_vcpu_t;

// TODO: use weak linking
extern "C" {
    pub fn _hv_vcpu_get_context(vcpu: hv_vcpu_t) -> *mut c_void;
}
