use std::{fs::File, os::fd::AsRawFd};

use anyhow::anyhow;
use libc::{F_RDLCK, F_UNLCK, F_WRLCK, SEEK_SET};
use nix::{errno::Errno, fcntl::{fcntl, flock, FcntlArg, FlockArg}};


// works by dropping file
pub struct FlockGuard<T> {
    _lock: Flock,
    data: T,
}

impl<T> FlockGuard<T> {
    pub fn new(lock: Flock, data: T) -> Self {
        FlockGuard { _lock: lock, data }
    }
}

impl<T> std::ops::Deref for FlockGuard<T> {
    type Target = T;

    fn deref(&self) -> &Self::Target {
        &self.data
    }
}

impl<T> std::ops::DerefMut for FlockGuard<T> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        &mut self.data
    }
}

pub struct Flock {
    _file: File,
}

impl Flock {
    pub fn new_nonblock_legacy_excl(file: File) -> anyhow::Result<Self> {
        match flock(file.as_raw_fd(), FlockArg::LockExclusiveNonblock) {
            Ok(_) => Ok(Flock { _file: file }),
            Err(Errno::EAGAIN) => Err(anyhow!("another instance of dctl is running")),
            Err(e) => Err(anyhow!("lock failed: {}", e)),
        }
    }

    pub fn new_ofd(file: File, mode: FlockMode, wait: FlockWait) -> nix::Result<Self> {
        let params = libc::flock {
            l_type: mode.to_type(),
            l_whence: SEEK_SET as i16,
            l_start: 0,
            l_len: 0,
            l_pid: 0,
        };
        fcntl(file.as_raw_fd(), match wait {
            FlockWait::Blocking => FcntlArg::F_OFD_SETLKW(&params),
            FlockWait::NonBlocking => FcntlArg::F_OFD_SETLK(&params),
        })?;
        Ok(Flock { _file: file })
    }

    pub fn check_ofd(file: File, mode: FlockMode) -> nix::Result<bool> {
        let mut params = libc::flock {
            l_type: mode.to_type(),
            l_whence: SEEK_SET as i16,
            l_start: 0,
            l_len: 0,
            l_pid: 0,
        };
        fcntl(file.as_raw_fd(), FcntlArg::F_OFD_GETLK(&mut params))?;
        Ok(params.l_type == F_UNLCK as i16)
    }
}

pub enum FlockMode {
    Exclusive,
    Shared,
}

impl FlockMode {
    fn to_type(self) -> i16 {
        match self {
            FlockMode::Exclusive => F_WRLCK as i16,
            FlockMode::Shared => F_RDLCK as i16,
        }
    }
}

pub enum FlockWait {
    Blocking,
    NonBlocking,
}
