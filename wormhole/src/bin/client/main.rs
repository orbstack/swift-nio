use anyhow::anyhow;
use nix::sys::socket::{sendmsg, ControlMessage, MsgFlags, UnixAddr};
use std::fs::{self, File};
use std::io::{stdin, stdout, IoSlice};
use std::os::fd::AsRawFd;
use std::os::unix::net::UnixStream;
use std::path::Path;
use wormhole::flock::{Flock, FlockMode, FlockWait};
use wormhole::rpc::RpcInputMessage;

const LOCK: &str = "/data/.lock";

fn connect_to_server() -> anyhow::Result<UnixStream> {
    let _flock = Flock::new_ofd(
        File::create(LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;

    // if !Path::new("/data/rpc.sock").exists() {
    //     RpcOutputMessage::StartServer().write_to_sync(&mut stdout())?;

    //     // wait until server starts
    //     match RpcInputMessage::read_from_sync(&mut stdin())? {
    //         RpcInputMessage::StartServerAck() => {}
    //         _ => return Err(anyhow!("expected StartServerAck")),
    //     };
    // }

    let stream = UnixStream::connect("/data/rpc.sock")
        .map_err(|e| anyhow!("could not connect to RPC socket: {}", e))?;

    // write connect server

    // match RpcInputMessage::read_from_sync(&mut stdin())? {
    //     RpcInputMessage::ConnectServerAck() => {}
    //     _ => return Err(anyhow!("expected ConnectServerAck")),
    // };

    Ok(stream)
}

fn main() -> anyhow::Result<()> {
    let stream = connect_to_server()?;

    let fds = [stdin().as_raw_fd(), stdout().as_raw_fd()];
    let cmsgs = [ControlMessage::ScmRights(&fds)];
    let iov = [IoSlice::new(&[0u8])];
    sendmsg::<()>(stream.as_raw_fd(), &iov, &cmsgs, MsgFlags::empty(), None)?;

    loop {}
}
