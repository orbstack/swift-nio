use std::{
    ffi::{c_char, CString},
    ptr::null_mut,
};

use libc::mmap;
use nix::{
    errno::Errno,
    sys::wait::{waitpid, WaitStatus},
    unistd::Pid,
};
use wormhole::err;

pub fn prctl_death_sig() -> anyhow::Result<()> {
    unsafe { err(libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL, 0, 0, 0))? };
    Ok(())
}

pub fn wait_for_exit(pid: Pid) -> anyhow::Result<()> {
    loop {
        let res = waitpid(pid, None)?;
        match res {
            WaitStatus::Exited(_, _) => break,
            WaitStatus::Signaled(_, _, _) => break,
            _ => {}
        }
    }

    Ok(())
}

pub fn set_cmdline_name(name: &str) -> anyhow::Result<()> {
    let cstr = CString::new(name)?;
    nix::sys::prctl::set_name(&cstr)?;

    // mmap a new argv
    let argv_start = unsafe {
        mmap(
            null_mut(),
            name.len() + 1,
            libc::PROT_READ | libc::PROT_WRITE,
            libc::MAP_ANONYMOUS | libc::MAP_PRIVATE,
            -1,
            0,
        )
    };
    if argv_start.is_null() {
        return Err(Errno::last().into());
    }
    let argv_start = argv_start as *mut c_char;

    unsafe {
        // copy null-terminated name
        std::ptr::copy_nonoverlapping(cstr.as_ptr(), argv_start, name.len() + 1);

        // set new argv
        let argv_end = argv_start.add(name.len() + 1);
        if err(libc::prctl(
            libc::PR_SET_MM,
            libc::PR_SET_MM_ARG_START,
            argv_start,
            0,
            0,
        ))
        .is_err()
        {
            // bounds check... have to set end first
            err(libc::prctl(
                libc::PR_SET_MM,
                libc::PR_SET_MM_ARG_END,
                argv_end,
                0,
                0,
            ))?;
            err(libc::prctl(
                libc::PR_SET_MM,
                libc::PR_SET_MM_ARG_START,
                argv_start,
                0,
                0,
            ))?;
        } else {
            // other case: start first
            err(libc::prctl(
                libc::PR_SET_MM,
                libc::PR_SET_MM_ARG_END,
                argv_end,
                0,
                0,
            ))?;
        }
    }

    Ok(())
}
