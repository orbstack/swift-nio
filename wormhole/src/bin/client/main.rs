use anyhow::anyhow;
use nix::sys::signal::{SigSet, Signal};
use nix::sys::signalfd::SignalFd;
use nix::sys::socket::{sendmsg, ControlMessage, MsgFlags, UnixAddr};
use nix::unistd::sleep;
use std::fs::{self, File};
use std::io::{stdin, stdout, IoSlice, Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::net::UnixStream;
use std::path::Path;
use tracing::{trace, Level};
use tracing_subscriber::fmt::format::FmtSpan;
use wormhole::flock::{Flock, FlockMode, FlockWait};
use wormhole::rpc::wormhole::rpc_server_message::ServerMessage;
use wormhole::rpc::wormhole::{RpcClientInit, RpcClientInitAck, RpcServerMessage};
use wormhole::rpc::{RpcReadSync, RpcWriteSync};

const LOCK: &str = "/data/.lock";
const RPC_SOCKET: &str = "/data/rpc.sock";

fn connect_to_server() -> anyhow::Result<UnixStream> {
    let _flock = Flock::new_ofd(
        File::create(LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;

    // tell scli process to start wormhole server if rpc socket does not exist
    let start_server = !Path::new(RPC_SOCKET).exists();
    RpcClientInit { start_server }.write_sync(&mut stdout())?;
    RpcClientInitAck::read_sync(&mut stdin())?;

    // wait for server to fully initialize and bind to socket
    while !Path::new(RPC_SOCKET).exists() {
        // sleep(1);
    }

    let mut stream = UnixStream::connect(RPC_SOCKET)
        .map_err(|e| anyhow!("could not connect to RPC socket: {}", e))?;

    // wait until the server increments its connection  and acknowledges our connection
    let message = RpcServerMessage::read_sync(&mut stream)?;
    match message.server_message {
        Some(ServerMessage::ClientConnectAck(_)) => {}
        _ => return Err(anyhow!("expected ClientConnectAck")),
    }

    Ok(stream)
}

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    let mut stream = connect_to_server()?;

    let fds = [stdin().as_raw_fd(), stdout().as_raw_fd()];
    let cmsgs = [ControlMessage::ScmRights(&fds)];
    let iov = [IoSlice::new(&[0u8])];
    sendmsg::<()>(stream.as_raw_fd(), &iov, &cmsgs, MsgFlags::empty(), None)?;

    // `docker stop <container>` still hangs, even when listening to SIGTERM... (?)
    // let mut mask = SigSet::empty();
    // mask.add(Signal::SIGTERM);
    // mask.thread_block()?;

    // let mut sfd = SignalFd::new(&mask)?;
    // loop {
    //     match sfd.read_signal()? {
    //         Some(siginfo) => {
    //             trace!("got sigterm");
    //             break;
    //         }
    //         _ => {}
    //     }
    // }

    // server drops the rpc socket connection when wormhole-attach exits
    stream.read(&mut [])?;
    Ok(())
}
