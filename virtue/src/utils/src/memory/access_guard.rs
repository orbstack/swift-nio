#![allow(clippy::missing_safety_doc)]

use std::{num::NonZeroUsize, ptr, sync::Once};

use thiserror::Error;

use crate::ffi::c_str::malloc_str;

// === FFI === //

#[repr(C)]
struct FaultState {
    region_base: libc::size_t,
    fault_addr: libc::size_t,
}

// Implemented in `utils/ffi/access_guard.c`.
extern "C" {
    fn orb_access_guard_init();

    fn orb_access_guard_signal_handler(
        signum: i32,
        info: *mut libc::siginfo_t,
        uap: *mut libc::c_void,
        userdata: *mut libc::c_void,
    ) -> libc::boolean_t;

    fn orb_access_guard_register_guarded_region(
        base: usize,
        len: usize,
        abort_msg: *const libc::c_char,
    );

    fn orb_access_guard_unregister_guarded_region(base: usize);

    fn orb_access_guard_start_catch();

    fn orb_access_guard_end_catch();

    fn orb_access_guard_check_for_errors() -> FaultState;
}

fn orb_access_guard_ensure_init() {
    static LOCK: Once = Once::new();

    LOCK.call_once(|| unsafe {
        orb_access_guard_init();

        sigstack::register_handler(
            libc::SIGBUS,
            orb_access_guard_signal_handler,
            ptr::null_mut(),
        )
        .unwrap();
    });
}

// === Wrappers === //

#[derive(Debug)]
pub struct GuardedRegion {
    base: usize,
}

impl GuardedRegion {
    pub unsafe fn new(base: *const u8, len: usize, abort_msg: &str) -> Self {
        orb_access_guard_ensure_init();
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
pub struct RegionCatchGuard {
    _no_send_sync: [*const (); 0],
}

impl Default for RegionCatchGuard {
    fn default() -> Self {
        Self::new()
    }
}

impl RegionCatchGuard {
    pub fn new() -> Self {
        unsafe { orb_access_guard_start_catch() };

        Self { _no_send_sync: [] }
    }
}

impl Drop for RegionCatchGuard {
    fn drop(&mut self) {
        unsafe { orb_access_guard_end_catch() };
    }
}

pub fn check_for_access_errors() -> Option<GuardedMemoryAccessError> {
    let res = unsafe { orb_access_guard_check_for_errors() };
    let region_base = NonZeroUsize::new(res.region_base)?;

    Some(GuardedMemoryAccessError {
        region_base,
        fault_addr: res.fault_addr,
    })
}

// === Higher Level Wrappers === //

#[derive(Debug, Clone, Error)]
#[error(
    "invalid memory operation in protected region at relative address 0x{:X} (region starts at 0x{:X})",
    self.fault_addr - self.region_base.get(),
    self.region_base,
)]
pub struct GuardedMemoryAccessError {
    pub region_base: NonZeroUsize,
    pub fault_addr: usize,
}

pub fn catch_access_errors<R>(f: impl FnOnce() -> R) -> Result<R, GuardedMemoryAccessError> {
    let guard = RegionCatchGuard::new();
    let res = f();
    drop(guard);

    if let Some(err) = check_for_access_errors() {
        return Err(err);
    }

    Ok(res)
}
