use anyhow::{anyhow, Result};
use libc::EBADF;
use nix::fcntl::fcntl;
use nix::fcntl::FcntlArg::F_GETFL;
use nix::sys::socket::{self, sendmsg, ControlMessage, MsgFlags, UnixAddr};
use nix::sys::uio;
use nix::unistd::sleep;
use std::io::{stdin, stdout, IoSlice, Read, Write};
use std::os::fd::{AsFd, AsRawFd};
use std::os::unix::net::UnixStream;

fn main() -> Result<()> {
    // sleep(3);
    println!("starting client");
    let mut stream = UnixStream::connect("/data/rpc.sock")
        .map_err(|e| anyhow!("Could not connect to RPC socket: {}", e))?;

    let fds = [stdin().as_raw_fd(), stdout().as_raw_fd()];
    let cmsgs = [ControlMessage::ScmRights(&fds)];
    let iov = [IoSlice::new(&[0u8])];
    sendmsg::<()>(stream.as_raw_fd(), &iov, &cmsgs, MsgFlags::empty(), None)?;

    // stdout().write_all(b"hello from client")?;

    // TODO: listen to server eof?
    // todo: don't spin?
    loop {}
}
