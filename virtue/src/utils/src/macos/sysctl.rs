use std::{ffi::CString, sync::OnceLock};

use nix::errno::Errno;

static OS_MAJOR_VERSION: OnceLock<u32> = OnceLock::new();

pub fn sysctl_string(name: &str) -> nix::Result<String> {
    let name = CString::new(name).unwrap();

    let mut len = 0;
    let ret = unsafe {
        libc::sysctlbyname(
            name.as_ptr(),
            std::ptr::null_mut(),
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    };
    Errno::result(ret)?;

    let mut buf = vec![0u8; len];
    let ret = unsafe {
        libc::sysctlbyname(
            name.as_ptr(),
            buf.as_mut_ptr() as *mut _,
            &mut len,
            std::ptr::null_mut(),
            0,
        )
    };
    Errno::result(ret)?;

    Ok(String::from_utf8_lossy(&buf).to_string())
}

pub fn os_major_version() -> u32 {
    *OS_MAJOR_VERSION.get_or_init(|| {
        sysctl_string("kern.osproductversion")
            .expect("failed to get OS version")
            .split('.')
            .next()
            .expect("failed to parse OS major version")
            .parse()
            .expect("failed to parse OS major version")
    })
}
