use std::{ffi::CString, time::Duration};

use libc::{pthread_mach_thread_np, pthread_self};
use mach2::thread_policy::{
    thread_policy_set, thread_time_constraint_policy_data_t, THREAD_TIME_CONSTRAINT_POLICY,
    THREAD_TIME_CONSTRAINT_POLICY_COUNT,
};
use nix::errno::Errno;
use utils::mach_time::MachAbsoluteDuration;

use crate::check_mach;

pub fn set_realtime_scheduling(interval: Duration) -> anyhow::Result<()> {
    let policy = thread_time_constraint_policy_data_t {
        period: 0,
        computation: MachAbsoluteDuration::from_duration(interval / 2).0 as u32,
        constraint: MachAbsoluteDuration::from_duration(interval).0 as u32,
        preemptible: 0,
    };

    unsafe {
        check_mach!(thread_policy_set(
            pthread_mach_thread_np(pthread_self()),
            THREAD_TIME_CONSTRAINT_POLICY,
            &policy as *const _ as *mut _,
            THREAD_TIME_CONSTRAINT_POLICY_COUNT,
        ))?;
    }
    Ok(())
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

    Ok(String::from_utf8_lossy(&buf).to_string())
}
