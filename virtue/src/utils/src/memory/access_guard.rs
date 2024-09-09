#![allow(clippy::missing_safety_doc)]

use crate::ffi::c_str::malloc_str;

// Implemented in `utils/ffi/access_guard.c`.
extern "C" {

    fn orb_access_guard_register_guarded_region(
        base: usize,
        len: usize,
        abort_msg: *const libc::c_char,
    );

    fn orb_access_guard_unregister_guarded_region(base: usize);

    fn orb_access_guard_start_catch();

    fn orb_access_guard_end_catch();

    fn orb_access_guard_check_for_errors() -> bool;
}

#[derive(Debug)]
pub struct GuardedRegion {
    base: usize,
}

impl GuardedRegion {
    pub unsafe fn new(base: *const u8, len: usize, abort_msg: &str) -> Self {
        orb_access_guard_register_guarded_region(base as usize, len, malloc_str(abort_msg));

        Self {
            base: base as usize,
        }
    }
}

impl Drop for GuardedRegion {
    fn drop(&mut self) {
        unsafe { orb_access_guard_unregister_guarded_region(self.base) };
    }
}

#[derive(Debug)]
pub struct RegionErrorCatchGuard {
    _no_send_sync: [*const (); 0],
}

impl Default for RegionErrorCatchGuard {
    fn default() -> Self {
        Self::new()
    }
}

impl RegionErrorCatchGuard {
    pub fn new() -> Self {
        unsafe { orb_access_guard_start_catch() };

        Self { _no_send_sync: [] }
    }
}

impl Drop for RegionErrorCatchGuard {
    fn drop(&mut self) {
        unsafe { orb_access_guard_end_catch() };
    }
}

pub fn check_for_access_errors() -> bool {
    unsafe { orb_access_guard_check_for_errors() }
}
