use std::os::fd::RawFd;

use crate::virtio::descriptor_utils::Iovec;

#[derive(Debug)]
pub enum ConnectError {}

#[derive(Debug)]
pub enum ReadError {
    /// Nothing was written
    NothingRead,
    /// Another internal error occurred
    Internal(nix::Error),
}

#[derive(Debug)]
pub enum WriteError {
    /// Nothing was written, you can drop the frame or try to resend it later
    NothingWritten,
    /// Passt doesnt seem to be running (received EPIPE)
    ProcessNotRunning,
    /// Another internal error occurred
    Internal(nix::Error),
}

pub trait NetBackend {
    fn read_frame(&mut self, buf: &mut [u8]) -> Result<usize, ReadError>;
    fn write_frame(&mut self, hdr_len: usize, iovs: &mut [Iovec]) -> Result<(), WriteError>;
    fn raw_socket_fd(&self) -> RawFd;
}
