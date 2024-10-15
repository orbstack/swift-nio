use anyhow::{anyhow, Result};
use nix::sys::socket::{self, sendmsg, ControlMessage, MsgFlags, UnixAddr};
use nix::sys::uio;
use std::io::{stdin, stdout, IoSlice};
use std::os::fd::{AsFd, AsRawFd};
use std::os::unix::net::UnixStream;

fn main() -> Result<()> {
    let stream = UnixStream::connect("/rpc.sock")
        .map_err(|e| anyhow!("Could not connect to RPC socket: {}", e))?;

    let fds = [stdin().as_raw_fd(), stdout().as_raw_fd()];
    let cmsgs = [ControlMessage::ScmRights(&fds)];
    let iov = [IoSlice::new(&[0u8])];
    sendmsg::<()>(stream.as_raw_fd(), &iov, &cmsgs, MsgFlags::empty(), None)?;

    // TODO: listen to server eof?
    loop {}
}
