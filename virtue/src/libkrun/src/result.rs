// exported to Go and Swift
#![allow(clippy::missing_safety_doc)]

use std::{
    ffi::{c_char, CString},
    os::raw::c_void,
};

use libc::strdup;

#[repr(C)]
pub struct GResultCreate {
    ptr: *mut c_void,
    err: *const c_char,
}

impl GResultCreate {
    pub fn from_result(r: anyhow::Result<*mut c_void>) -> GResultCreate {
        match r {
            Ok(ptr) => GResultCreate {
                ptr,
                err: std::ptr::null(),
            },
            Err(e) => GResultCreate {
                ptr: std::ptr::null_mut(),
                err: return_owned_cstr(&e.to_string()),
            },
        }
    }
}

#[repr(C)]
pub struct GResultErr {
    err: *const c_char,
}

impl GResultErr {
    pub fn from_result<T>(r: Result<T, anyhow::Error>) -> GResultErr {
        match r {
            Ok(_) => GResultErr {
                err: std::ptr::null(),
            },
            Err(e) => GResultErr {
                err: return_owned_cstr(&e.to_string()),
            },
        }
    }
}

#[repr(C)]
#[allow(dead_code)]
pub struct GResultIntErr {
    value: i64,
    err: *const c_char,
}

fn return_owned_cstr(s: &str) -> *const c_char {
    // important: copy and leak the newly allocated string
    let s = CString::new(s).unwrap();
    // required to make it safe to free from C if rust isn't using system allocator
    unsafe { strdup(s.as_ptr()) }
}
