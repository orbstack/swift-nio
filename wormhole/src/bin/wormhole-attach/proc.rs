use std::{
    ffi::{c_char, CString},
    os::fd::{AsRawFd, BorrowedFd},
    ptr::null_mut,
};

use libc::mmap;
use nix::{
    dir::Dir,
    errno::Errno,
    fcntl::{AtFlags, OFlag},
    sys::{
        signal::Signal,
        stat::{fstatat, Mode},
        wait::{waitid, waitpid, Id, WaitPidFlag, WaitStatus},
    },
    unistd::Pid,
};
use wormhole::err;

pub fn prctl_death_sig() -> anyhow::Result<()> {
    unsafe { err(libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL, 0, 0, 0))? };
    Ok(())
}

pub enum ExitResult {
    Code(i32),
    #[allow(dead_code)]
    Signal(Signal),
}

pub fn wait_for_exit<P: Into<Option<Pid>>>(pid: P) -> anyhow::Result<ExitResult> {
    let pid: Option<Pid> = pid.into();
    loop {
        let res = waitpid(pid, None)?;
        match res {
            WaitStatus::Exited(_, exit_code) => return Ok(ExitResult::Code(exit_code)),
            WaitStatus::Signaled(_, signal, _) => return Ok(ExitResult::Signal(signal)),
            _ => {}
        }
    }
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

pub fn iter_pids_from_dirfd(
    proc_fd: BorrowedFd<'_>,
) -> Result<impl Iterator<Item = Result<Pid, Errno>>, Errno> {
    Ok(Dir::openat(
        proc_fd.as_raw_fd(),
        "./",
        OFlag::O_DIRECTORY | OFlag::O_RDONLY,
        Mode::empty(),
    )?
    .into_iter()
    .filter_map(|direntry| match direntry {
        Ok(direntry) => {
            if !direntry
                .file_type()
                .is_some_and(|filetype| matches!(filetype, nix::dir::Type::Directory))
            {
                return None;
            }

            let Ok(file_name) = CString::from(direntry.file_name()).into_string() else {
                return None;
            };

            let Ok(pid) = file_name.parse::<u32>() else {
                return None;
            };

            Some(Ok(Pid::from_raw(pid as i32)))
        }
        Err(err) => Some(Err(err)),
    }))
}

pub fn get_ns_of_pid_from_dirfd(
    proc_fd: BorrowedFd<'_>,
    pid: Pid,
    ns: &'static str,
) -> Result<u64, Errno> {
    Ok(fstatat(
        proc_fd.as_raw_fd(),
        format!("./{}/ns/{}", pid, ns).as_str(),
        AtFlags::empty(),
    )?
    .st_ino)
}

/// Returns a boolean indicating whether child processes still exist
pub fn reap_children(mut process_exited_cb: impl FnMut(Pid, i32)) -> Result<bool, Errno> {
    loop {
        match waitid(Id::All, WaitPidFlag::WNOHANG | WaitPidFlag::WEXITED) {
            Ok(WaitStatus::Exited(pid, status)) => process_exited_cb(pid, status),
            Ok(WaitStatus::Signaled(pid, signal, _)) => process_exited_cb(pid, signal as i32 + 128),
            Ok(WaitStatus::StillAlive) => return Ok(true),
            Ok(_) => {}
            Err(Errno::ECHILD) => return Ok(false),
            Err(err) => return Err(err),
        }
    }
}
