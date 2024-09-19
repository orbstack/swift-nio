use std::{
    mem::{size_of, MaybeUninit},
    os::{
        fd::{AsFd, AsRawFd, BorrowedFd, FromRawFd, OwnedFd},
        raw::c_int,
    },
    ptr::{null_mut},
};

use libc::{signalfd_siginfo, sigset_t};

pub struct SigSet(libc::sigset_t);

impl SigSet {
    pub fn empty() -> Result<Self, std::io::Error> {
        unsafe {
            let mut sigset = MaybeUninit::<sigset_t>::uninit();
            if libc::sigemptyset(sigset.as_mut_ptr()) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(Self(sigset.assume_init()))
        }
    }

    pub fn add_signal(&mut self, signal: i32) -> Result<(), std::io::Error> {
        unsafe {
            if libc::sigaddset(&mut self.0 as *mut sigset_t, signal) < 0 {
                return Err(std::io::Error::last_os_error());
            }
        }
        Ok(())
    }
}

pub fn mask_sigset(sigset: &SigSet, how: c_int) -> Result<(), std::io::Error> {
    unsafe {
        if libc::sigprocmask(how, &sigset.0 as *const sigset_t, null_mut()) < 0 {
            return Err(std::io::Error::last_os_error());
        }
    }

    Ok(())
}

pub struct SignalFd(OwnedFd);

impl SignalFd {
    pub fn new(sigset: &SigSet, flags: c_int) -> Result<Self, std::io::Error> {
        unsafe {
            let signalfd = libc::signalfd(-1, &sigset.0 as *const sigset_t, flags);
            if signalfd < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(Self(OwnedFd::from_raw_fd(signalfd)))
        }
    }

    pub fn read_signal(&mut self) -> Result<Option<signalfd_siginfo>, std::io::Error> {
        unsafe {
            let mut siginfo = MaybeUninit::<signalfd_siginfo>::uninit();

            // assume full read
            match libc::read(
                self.0.as_raw_fd(),
                siginfo.as_mut_ptr() as *mut libc::c_void,
                size_of::<signalfd_siginfo>(),
            ) {
                x if x == size_of::<signalfd_siginfo>() as isize => {
                    Ok(Some(siginfo.assume_init()))
                }
                x if x < 0
                    && std::io::Error::last_os_error().raw_os_error().unwrap() == libc::EAGAIN =>
                {
                    Ok(None)
                }
                x if x < 0 => Err(std::io::Error::last_os_error()),
                _ => panic!("partial read from signalfd"),
            }
        }
    }
}

impl AsFd for SignalFd {
    fn as_fd(&self) -> BorrowedFd<'_> {
        self.0.as_fd()
    }
}

impl From<SignalFd> for OwnedFd {
    fn from(value: SignalFd) -> Self {
        value.0
    }
}
