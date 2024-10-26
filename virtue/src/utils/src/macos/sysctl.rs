use std::{
    ffi::{CStr, CString},
    sync::LazyLock,
};

use nix::errno::Errno;

pub static OS_VERSION: LazyLock<OsVersion> = LazyLock::new(|| {
    let str = sysctl_string("kern.osproductversion").expect("failed to get OS version");
    let mut split = str.split('.');

    let major = split
        .next()
        .and_then(|s| s.parse().ok())
        .expect("failed to parse OS major version");
    let minor = split
        .next()
        .and_then(|s| s.parse().ok())
        .expect("failed to parse OS minor version");

    OsVersion { major, minor }
});

#[derive(Debug, Clone, Copy)]
pub struct OsVersion {
    pub major: u32,
    pub minor: u32,
}

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

    Ok(CStr::from_bytes_with_nul(&buf)
        .unwrap()
        .to_string_lossy()
        .to_string())
}
