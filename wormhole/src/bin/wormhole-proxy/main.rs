use nix::sys::socket::{sendmsg, ControlMessage, MsgFlags};
use std::env;
use std::io::{stdin, stdout, IoSlice, Read};
use std::os::fd::AsRawFd;
use std::os::linux::net::SocketAddrExt as _;
use std::os::unix::net::{SocketAddr, UnixStream};
use std::process::exit;
use wormhole::rpc::RPC_SOCKET;

const VERSION_MISMATCH_EXIT_CODE: i32 = 123;

fn check_version_compatibility() -> anyhow::Result<()> {
    // server version is set in the wormhole image, client version is passed during exec create request
    let client_version = semver::Version::parse(&env::var("WORMHOLE_CLIENT_VERSION")?)?;
    let server_version = semver::Version::parse(&env::var("WORMHOLE_SERVER_VERSION")?)?;

    // server maintains backward compatibility with older client versions. However, forward
    // compatibility is not guaranteed.
    if client_version <= server_version {
        Ok(())
    } else {
        Err(anyhow::anyhow!(
            "client version {} is not supported by server version {}",
            client_version,
            server_version
        ))
    }
}

fn main() -> anyhow::Result<()> {
    if let Err(_) = check_version_compatibility() {
        exit(VERSION_MISMATCH_EXIT_CODE);
    }

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
