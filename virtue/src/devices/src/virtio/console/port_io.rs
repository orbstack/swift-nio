use std::fs::File;
use std::io;
use std::os::fd::{AsFd, AsRawFd, OwnedFd, RawFd};

use libc::{fcntl, F_GETFL, F_SETFL, O_NONBLOCK, STDERR_FILENO, STDIN_FILENO, STDOUT_FILENO};
use nix::errno::Errno;
use utils::fd::dup_fd;
use utils::memory::{GuestSlice, WritePartialFd};

pub trait PortInput {
    fn read_volatile(&mut self, buf: GuestSlice<'_, u8>) -> Result<usize, io::Error>;

    fn ready_fd(&self) -> Option<RawFd>;
}

pub trait PortOutput {
    fn write_volatile(&mut self, buf: GuestSlice<'_, u8>) -> Result<usize, io::Error>;

    fn ready_fd(&self) -> Option<RawFd>;
}

pub fn stdin() -> Result<Box<dyn PortInput + Send>, nix::Error> {
    let fd = dup_fd(STDIN_FILENO)?;
    make_non_blocking(&fd)?;
    Ok(Box::new(PortInputFd(fd)))
}

pub fn stdout() -> Result<Box<dyn PortOutput + Send>, nix::Error> {
    output_to_raw_fd_dup(STDOUT_FILENO)
}

pub fn stderr() -> Result<Box<dyn PortOutput + Send>, nix::Error> {
    output_to_raw_fd_dup(STDERR_FILENO)
}

pub fn input_empty() -> Result<Box<dyn PortInput + Send>, nix::Error> {
    Ok(Box::new(PortInputEmpty {}))
}

pub fn input_from_raw_fd_dup(fd: RawFd) -> Result<Box<dyn PortInput + Send>, nix::Error> {
    let fd = dup_fd(fd)?;
    make_non_blocking(&fd)?;
    Ok(Box::new(PortInputFd(fd)))
}

pub fn output_file(file: File) -> Result<Box<dyn PortOutput + Send>, nix::Error> {
    output_to_raw_fd_dup(file.as_raw_fd())
}

pub fn output_to_raw_fd_dup(fd: RawFd) -> Result<Box<dyn PortOutput + Send>, nix::Error> {
    let fd = dup_fd(fd)?;
    Ok(Box::new(PortOutputFd(fd)))
}

pub fn output_to_log_as_err() -> Box<dyn PortOutput + Send> {
    Box::new(PortOutputLog::new())
}

struct PortInputFd(OwnedFd);

impl AsRawFd for PortInputFd {
    fn as_raw_fd(&self) -> RawFd {
        self.0.as_raw_fd()
    }
}

impl PortInput for PortInputFd {
    fn read_volatile(&mut self, buf: GuestSlice<'_, u8>) -> io::Result<usize> {
        // This source code is copied from vm-memory, except it fixes an issue, where
        // the original code would does not handle handle EWOULDBLOCK

        let fd = self.as_raw_fd();
        let dst = buf.as_ptr().cast::<libc::c_void>();

        // SAFETY: We got a valid file descriptor from `AsRawFd`. The memory pointed to by `dst` is
        // valid for writes of length `buf.len() by the invariants upheld by the constructor
        // of `VolatileSlice`.
        let bytes_read = unsafe { libc::read(fd, dst, buf.len()) };

        if bytes_read < 0 {
            Err(io::Error::last_os_error())
        } else {
            let bytes_read = bytes_read.try_into().unwrap();
            Ok(bytes_read)
        }
    }

    fn ready_fd(&self) -> Option<RawFd> {
        Some(self.as_raw_fd())
    }
}

struct PortOutputFd(OwnedFd);

impl AsRawFd for PortOutputFd {
    fn as_raw_fd(&self) -> RawFd {
        self.0.as_raw_fd()
    }
}

impl PortOutput for PortOutputFd {
    fn write_volatile(&mut self, buf: GuestSlice<'_, u8>) -> Result<usize, io::Error> {
        buf.write_from_guest(&mut WritePartialFd(self.0.as_fd()))
    }

    fn ready_fd(&self) -> Option<RawFd> {
        Some(self.as_raw_fd())
    }
}

fn make_non_blocking(as_rw_fd: &impl AsRawFd) -> Result<(), nix::Error> {
    let fd = as_rw_fd.as_raw_fd();
    unsafe {
        let flags = fcntl(fd, F_GETFL, 0);
        if flags < 0 {
            return Err(Errno::last());
        }

        if fcntl(fd, F_SETFL, flags | O_NONBLOCK) < 0 {
            return Err(Errno::last());
        }
    }
    Ok(())
}

// Utility to relay log from the VM (the kernel boot log and messages from init)
// to the rust log
#[derive(Default)]
pub struct PortOutputLog {
    buf: Vec<u8>,
}

impl PortOutputLog {
    const FORCE_FLUSH_TRESHOLD: usize = 512;
    const LOG_TARGET: &'static str = "init_or_kernel";

    fn new() -> Self {
        Self::default()
    }

    fn force_flush(&mut self) {
        tracing::error!(target: PortOutputLog::LOG_TARGET, "[missing newline]{}", String::from_utf8_lossy(&self.buf));
        self.buf.clear();
    }
}

impl PortOutput for PortOutputLog {
    fn write_volatile(&mut self, buf: GuestSlice<'_, u8>) -> Result<usize, io::Error> {
        buf.write_from_guest(&mut self.buf);

        let mut start = 0;
        for (i, ch) in self.buf.iter().cloned().enumerate() {
            if ch == b'\n' {
                tracing::error!(target: PortOutputLog::LOG_TARGET, "{}", String::from_utf8_lossy(&self.buf[start..i]));
                start = i + 1;
            }
        }
        self.buf.drain(0..start);
        // Make sure to not grow the internal buffer forever!
        if self.buf.len() > PortOutputLog::FORCE_FLUSH_TRESHOLD {
            self.force_flush()
        }
        Ok(buf.len())
    }

    fn ready_fd(&self) -> Option<RawFd> {
        None
    }
}

pub struct PortInputEmpty {}

impl PortInputEmpty {
    pub fn new() -> Self {
        PortInputEmpty {}
    }
}

impl Default for PortInputEmpty {
    fn default() -> Self {
        Self::new()
    }
}

impl PortInput for PortInputEmpty {
    fn read_volatile(&mut self, _buf: GuestSlice<'_, u8>) -> Result<usize, io::Error> {
        Ok(0)
    }

    fn ready_fd(&self) -> Option<RawFd> {
        None
    }
}
