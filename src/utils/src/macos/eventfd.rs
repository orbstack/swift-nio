// Copyright 2021 Sergio Lopez. All rights reserved.
//
// Copyright 2020 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// SPDX-License-Identifier: (Apache-2.0 AND BSD-3-Clause)

//! Structure and wrapper functions emulating eventfd using a pipe.

use std::os::unix::io::{AsRawFd, RawFd};
use std::{io, result};

use libc::{c_void, fcntl, pipe, read, write, FD_CLOEXEC, F_GETFL, F_SETFD, F_SETFL, O_NONBLOCK};

pub const EFD_NONBLOCK: i32 = 1;

fn set_nonblock(fd: RawFd) -> io::Result<()> {
    let flags = unsafe { fcntl(fd, F_GETFL) };
    if flags < 0 {
        return Err(io::Error::last_os_error());
    }

    let ret = unsafe { fcntl(fd, F_SETFL, flags | O_NONBLOCK) };
    if ret < 0 {
        return Err(io::Error::last_os_error());
    }

    Ok(())
}

fn set_cloexec(fd: RawFd) -> io::Result<()> {
    let ret = unsafe { fcntl(fd, F_SETFD, FD_CLOEXEC) };
    if ret < 0 {
        return Err(io::Error::last_os_error());
    }

    Ok(())
}

#[derive(Debug)]
pub struct EventFd {
    read_fd: RawFd,
    write_fd: RawFd,
}

impl EventFd {
    pub fn new(flag: i32) -> io::Result<EventFd> {
        let mut fds: [RawFd; 2] = [0, 0];
        let ret = unsafe { pipe(&mut fds[0]) };
        if ret < 0 {
            return Err(io::Error::last_os_error());
        }

        set_cloexec(fds[0])?;
        set_cloexec(fds[1])?;

        if flag == EFD_NONBLOCK {
            set_nonblock(fds[0])?;
            set_nonblock(fds[1])?;
        }

        Ok(EventFd {
            read_fd: fds[0],
            write_fd: fds[1],
        })
    }

    pub fn write(&self) -> io::Result<()> {
        let data: [u8; 8] = u64::to_ne_bytes(1);
        let ret = unsafe { write(self.write_fd, data.as_ptr() as *const c_void, data.len()) };
        if ret <= 0 {
            let error = io::Error::last_os_error();
            match error.kind() {
                // We may get EAGAIN if the eventfd is overstimulated, but we can safely
                // ignore it as we can be sure the subscriber will get notified.
                io::ErrorKind::WouldBlock => Ok(()),
                _ => Err(error),
            }
        } else {
            Ok(())
        }
    }

    pub fn read(&self) -> io::Result<()> {
        loop {
            let mut buf = [0u8; 1024];
            let ret = unsafe { read(self.read_fd, buf.as_mut_ptr() as *mut c_void, buf.len()) };
            if ret < 0 {
                let err = io::Error::last_os_error();

                if let io::ErrorKind::WouldBlock = err.kind() {
                    // (we consumed all the signal assertions in this queue)
                    break;
                }

                return Err(err);
            }

            // (we can still read!)
        }

        Ok(())
    }

    pub fn try_clone(&self) -> result::Result<EventFd, io::Error> {
        let read_fd = unsafe { fcntl(self.read_fd, libc::F_DUPFD_CLOEXEC, 3) };
        if read_fd < 0 {
            return Err(io::Error::last_os_error());
        }

        let write_fd = unsafe { fcntl(self.write_fd, libc::F_DUPFD_CLOEXEC, 3) };
        if write_fd < 0 {
            return Err(io::Error::last_os_error());
        }

        Ok(EventFd { read_fd, write_fd })
    }

    pub fn get_write_fd(&self) -> RawFd {
        self.write_fd
    }
}

impl AsRawFd for EventFd {
    fn as_raw_fd(&self) -> RawFd {
        self.read_fd
    }
}
