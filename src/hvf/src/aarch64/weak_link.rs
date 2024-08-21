use dlopen_derive::WrapperApi;
use once_cell::sync::Lazy;

use dlopen::wrapper::{Container, WrapperApi};

use super::bindings::{hv_gic_config_t, hv_gic_intid_t, hv_ipa_t, hv_return_t, hv_vm_config_t};

// macOS 13+ APIs
pub static OPTIONAL13: Lazy<Option<Container<HvfOptional13>>> =
    Lazy::new(|| unsafe { Container::load_self() }.ok());

// macOS 15+ APIs
pub static OPTIONAL15: Lazy<Option<Container<HvfOptional15>>> =
    Lazy::new(|| unsafe { Container::load_self() }.ok());

#[derive(WrapperApi)]
pub struct HvfOptional13 {
    hv_vm_config_create: unsafe extern "C" fn() -> hv_vm_config_t,
    hv_vm_config_get_max_ipa_size: unsafe extern "C" fn(ipa_bit_length: *mut u32) -> hv_return_t,
    hv_vm_config_get_default_ipa_size:
        unsafe extern "C" fn(ipa_bit_length: *mut u32) -> hv_return_t,
    hv_vm_config_set_ipa_size:
        unsafe extern "C" fn(config: hv_vm_config_t, ipa_bit_length: u32) -> hv_return_t,
}

#[derive(WrapperApi)]
pub struct HvfOptional15 {
    hv_gic_config_create: unsafe extern "C" fn() -> hv_gic_config_t,
    hv_gic_get_distributor_size: unsafe extern "C" fn(distributor_size: *mut usize) -> hv_return_t,
    hv_gic_get_redistributor_region_size:
        unsafe extern "C" fn(redistributor_region_size: *mut usize) -> hv_return_t,
    hv_gic_get_intid:
        unsafe extern "C" fn(interrupt: hv_gic_intid_t, intid: *mut u32) -> hv_return_t,
    hv_gic_config_set_distributor_base: unsafe extern "C" fn(
        config: hv_gic_config_t,
        distributor_base_address: hv_ipa_t,
    ) -> hv_return_t,
    hv_gic_config_set_redistributor_base: unsafe extern "C" fn(
        config: hv_gic_config_t,
        redistributor_base_address: hv_ipa_t,
    ) -> hv_return_t,
    hv_gic_create: unsafe extern "C" fn(config: hv_gic_config_t) -> hv_return_t,

    hv_gic_set_spi: unsafe extern "C" fn(intid: u32, level: bool) -> hv_return_t,

    hv_vm_config_set_el2_enabled:
        unsafe extern "C" fn(config: hv_vm_config_t, el2_enabled: bool) -> hv_return_t,
}

#[macro_export]
macro_rules! call_optional {
    ($optional:ident.$method:ident($($args: expr),*)) => {
        $optional.as_ref().unwrap().$method($($args),*)
    };
}
