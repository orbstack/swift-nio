use nix::sys::socket::{sendmsg, ControlMessage, MsgFlags};
use std::io::{stdin, stdout, IoSlice, Read};
use std::os::fd::AsRawFd;
use std::os::linux::net::SocketAddrExt as _;
use std::os::unix::net::{SocketAddr, UnixStream};
use wormhole::rpc::RPC_SOCKET;

fn main() -> anyhow::Result<()> {
    let addr = SocketAddr::from_abstract_name(RPC_SOCKET)?;
    let mut stream = UnixStream::connect_addr(&addr)?;

    // stdin/stdout are used as the RPC pipes between client and server
    let fds = [stdin().as_raw_fd(), stdout().as_raw_fd()];
    let cmsgs = [ControlMessage::ScmRights(&fds)];
    let iov = [IoSlice::new(&[0u8])];
    sendmsg::<()>(stream.as_raw_fd(), &iov, &cmsgs, MsgFlags::empty(), None)?;

    // server drops the rpc socket connection when wormhole-attach exits
    loop {
        match stream.read(&mut [0u8]) {
            Ok(0) => break,
            Ok(_) => {}
            Err(e) => return Err(e.into()),
        }
    }

    // TODO: forward client signals to server

    Ok(())
}
