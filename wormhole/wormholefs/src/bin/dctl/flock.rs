use std::{fs::File, os::fd::AsRawFd};

use anyhow::anyhow;
use nix::{errno::Errno, fcntl::{flock, FlockArg}};


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
    pub fn new_nonblock_path(path: &str) -> anyhow::Result<Self> {
        let file = File::open(path)?;
        Ok(Self::new_nonblock_file(file)?)
    }

    pub fn new_nonblock_file(file: File) -> anyhow::Result<Self> {
        match flock(file.as_raw_fd(), FlockArg::LockExclusiveNonblock) {
            Ok(_) => Ok(Flock { _file: file }),
            Err(Errno::EAGAIN) => Err(anyhow!("another instance of dctl is running")),
            Err(e) => Err(anyhow!("lock failed: {}", e)),
        }
    }
}
