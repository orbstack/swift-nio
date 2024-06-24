use nix::errno::Errno;
use nix::fcntl::{fcntl, FcntlArg, OFlag};
use nix::sys::uio::writev;
use std::os::fd::{AsRawFd, RawFd};

use crate::virtio::descriptor_utils::Iovec;

use super::backend::{ConnectError, NetBackend, ReadError, WriteError};

fn readv<F: AsRawFd>(fd: F, iov: &[Iovec]) -> Result<usize, Errno> {
    let ret = unsafe {
        libc::readv(
            fd.as_raw_fd(),
            iov.as_ptr() as *const libc::iovec,
            iov.len() as i32,
        )
    };
    Errno::result(ret).map(|r| r as usize)
}

pub struct Dgram {
    fd: RawFd,
}

impl Dgram {
    pub fn new(fd: RawFd) -> Result<Self, ConnectError> {
        // macOS forces us to do this here instead of just using SockFlag::SOCK_NONBLOCK above.
        match fcntl(fd, FcntlArg::F_GETFL) {
            Ok(flags) => match OFlag::from_bits(flags) {
                Some(flags) => {
                    if let Err(e) = fcntl(fd, FcntlArg::F_SETFL(flags | OFlag::O_NONBLOCK)) {
                        warn!("error switching to non-blocking: id={}, err={}", fd, e);
                    }
                }
                None => error!("invalid fd flags id={}", fd),
            },
            Err(e) => error!("couldn't obtain fd flags id={}, err={}", fd, e),
        };

        #[cfg(target_os = "macos")]
        {
            // nix doesn't provide an abstraction for SO_NOSIGPIPE, fall back to libc.
            let option_value: libc::c_int = 1;
            unsafe {
                libc::setsockopt(
                    fd,
                    libc::SOL_SOCKET,
                    libc::SO_NOSIGPIPE,
                    &option_value as *const _ as *const libc::c_void,
                    std::mem::size_of_val(&option_value) as libc::socklen_t,
                )
            };
        }

        Ok(Self { fd })
    }
}

impl NetBackend for Dgram {
    /// Try to read a frame from passt. If no bytes are available reports ReadError::NothingRead
    fn read_frame(&mut self, buf: &[Iovec]) -> Result<usize, ReadError> {
        let frame_length = match readv(self.fd, buf) {
            Ok(f) => f,
            Err(Errno::EAGAIN) => return Err(ReadError::NothingRead),
            Err(e) => {
                return Err(ReadError::Internal(e));
            }
        };
        debug!("Read eth frame from passt: {} bytes", frame_length);
        Ok(frame_length)
    }

    /// Try to write a frame to passt.
    /// (Will mutate and override parts of buf, with a passt header!)
    ///
    /// * `hdr_len` - specifies the size of any existing headers encapsulating the ethernet frame,
    ///               (such as vnet header), that can be overwritten.
    ///               must be >= PASST_HEADER_LEN
    /// * `buf` - the buffer to write to passt, `buf[..hdr_len]` may be overwritten
    fn write_frame(&mut self, hdr_len: usize, iovs: &mut [Iovec]) -> Result<(), WriteError> {
        // skip header
        if !iovs.is_empty() && iovs[0].len() >= hdr_len {
            iovs[0].advance(hdr_len);
        } else {
            return Err(WriteError::Internal(nix::Error::EINVAL));
        }

        match writev(self.fd, Iovec::slice_to_std(iovs)) {
            Ok(_) => Ok(()),
            Err(Errno::ENOBUFS | Errno::EAGAIN) => Err(WriteError::NothingWritten),
            Err(Errno::EPIPE) => Err(WriteError::ProcessNotRunning),
            Err(e) => Err(WriteError::Internal(e)),
        }
    }

    fn raw_socket_fd(&self) -> RawFd {
        self.fd.as_raw_fd()
    }
}
