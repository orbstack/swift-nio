use anyhow::anyhow;
use nix::sys::socket::{sendmsg, ControlMessage, MsgFlags};
use std::io::{stdin, stdout, IoSlice, Read};
use std::os::fd::AsRawFd;
use std::os::unix::net::UnixStream;
const RPC_SOCKET: &str = "/data/rpc.sock";

fn main() -> anyhow::Result<()> {
    let mut stream = UnixStream::connect(RPC_SOCKET)
        .map_err(|e| anyhow!("could not connect to RPC socket: {}", e))?;

    let fds = [stdin().as_raw_fd(), stdout().as_raw_fd()];
    let cmsgs = [ControlMessage::ScmRights(&fds)];
    let iov = [IoSlice::new(&[0u8])];
    sendmsg::<()>(stream.as_raw_fd(), &iov, &cmsgs, MsgFlags::empty(), None)?;

    // server drops the rpc socket connection when wormhole-attach exits
    stream.read(&mut [])?;
    // loop {}
    Ok(())
}
