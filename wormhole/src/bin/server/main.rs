use anyhow::anyhow;
use libc::{winsize, MS_PRIVATE, TIOCSCTTY, TIOCSWINSZ};
use std::{
    ffi::CString,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd, RawFd},
        unix::net::{UnixListener, UnixStream},
    },
    path::Path,
    process::exit,
    sync::Arc,
    thread,
    time::Duration,
};
use tokio::{
    io::{self, AsyncReadExt, AsyncWriteExt},
    process::Command,
    sync::Mutex,
    task::{self, JoinHandle},
    time::sleep,
};
use tokio_util::sync::CancellationToken;
use tracing_subscriber::fmt::format::FmtSpan;
use util::{mount_wormhole, unmount_wormhole};
use wormhole::{
    asyncfile::AsyncFile,
    flock::{Flock, FlockMode, FlockWait},
    model::WormholeConfig,
    newmount::{mount_setattr, open_tree, MountAttr},
    rpc::{
        wormhole::{
            rpc_client_message::ClientMessage, rpc_server_message::ServerMessage, ClientConnectAck,
            ExitStatus, RpcClientMessage, RpcServerMessage, StderrData, StdoutData,
        },
        RpcRead, RpcWrite, RpcWriteSync,
    },
    termios::create_pty,
    unset_cloexec,
};

use nix::{
    libc::ioctl,
    mount::{mount, MsFlags},
    pty::{OpenptyResult, Winsize},
    sys::socket::{recvmsg, ControlMessageOwned, MsgFlags},
    unistd::{dup, dup2, pipe, setsid},
};
use tracing::{trace, Level};

mod server;
mod util;
use serde::{Deserialize, Serialize};

const REFCOUNT_FILE: &str = "/data/refcount";
const REFCOUNT_LOCK: &str = "/data/refcount.lock";

// receives client stdin and stdout fds  with SCM_RIGHTS
fn recv_rpc_client(stream: &UnixStream) -> anyhow::Result<(AsyncFile, AsyncFile)> {
    let mut buf = [0u8; 1];
    let mut cmsgspace = nix::cmsg_space!([RawFd; 2]);

    let mut iov = [std::io::IoSliceMut::new(&mut buf)];
    let msg = recvmsg::<()>(
        stream.as_raw_fd(),
        &mut iov,
        Some(&mut cmsgspace),
        MsgFlags::empty(),
    )?;

    let mut stdin_fd: Option<RawFd> = None;
    let mut stdout_fd: Option<RawFd> = None;

    for cmsg in msg.cmsgs() {
        if let ControlMessageOwned::ScmRights(fds) = cmsg {
            if fds.len() == 2 {
                stdin_fd = Some(fds[0]);
                stdout_fd = Some(fds[1]);
            }
        }
    }
    match (stdin_fd, stdout_fd) {
        (Some(stdin_fd), Some(stdout_fd)) => {
            let client_stdin = AsyncFile::new(stdin_fd)?;
            let client_stdout = AsyncFile::new(stdout_fd)?;
            Ok((client_stdin, client_stdout))
        }
        _ => Err(anyhow!("did not get client stdin and stdout")),
    }
}

// #[derive(Copy)]
struct WormholeServer {
    conn: Mutex<u32>,
}

impl WormholeServer {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            conn: Mutex::new(0),
        })
    }

    fn init(self: &Arc<Self>) -> anyhow::Result<()> {
        trace!("initializing wormhole");
        mount_wormhole()
    }

    fn listen(self: &Arc<Self>) -> anyhow::Result<()> {
        let _ = std::fs::remove_file("/data/rpc.sock");
        let listener = UnixListener::bind("/data/rpc.sock")?;

        loop {
            match listener.accept() {
                Ok((stream, _)) => {
                    let server = self.clone();
                    tokio::spawn(async move {
                        match server.handle_client(stream).await {
                            Ok(_) => {}
                            Err(e) => {
                                trace!("error handling client: {:?}", e);
                            }
                        }
                    });
                }
                Err(e) => {
                    trace!("error accepting connection: {:?}", e);
                }
            }
        }
    }

    async fn handle_client(self: Arc<Self>, mut stream: UnixStream) -> anyhow::Result<()> {
        // increment refcount and acknowledge client
        {
            let mut lock = self.conn.lock().await;
            *lock += 1;
            trace!("new connection; refcount is now {}", *lock);
        }

        // get client fds via scm_rights over the stream
        trace!("waiting for rpc client fds");
        let (mut client_stdin, mut client_stdout) = recv_rpc_client(&stream)?;
        trace!(
            "got rpc client fds {} {}",
            client_stdin.as_raw_fd(),
            client_stdout.as_raw_fd()
        );
        RpcServerMessage {
            server_message: Some(ServerMessage::ClientConnectAck(ClientConnectAck {})),
        }
        .write(&mut client_stdout)
        .await?;

        let client_writer_m = Arc::new(Mutex::new(client_stdout));
        let mut pty: Option<OpenptyResult> = None;
        let mut term_env: Option<String> = None;

        let wormhole_param;

        let mut stdin_pipe: (RawFd, RawFd) = (-1, -1);
        let mut stdout_pipe: (RawFd, RawFd) = (-1, -1);
        let mut stderr_pipe: (RawFd, RawFd) = (-1, -1);

        // start the rpc server that accepts commands like sendPayloadStdin

        // get the client capability, so that we can send back payload stdout, etc.

        // wait until user calls start before proceeding
        loop {
            let message = RpcClientMessage::read(&mut client_stdin).await?;

            match message.client_message {
                Some(ClientMessage::RequestPty(msg)) => {
                    pty = Some(create_pty(msg.cols as u16, msg.rows as u16, msg.termios)?);
                    term_env = Some(msg.term_env);

                    let slave_fd = pty.as_ref().unwrap().slave.as_raw_fd();
                    let master_fd = pty.as_ref().unwrap().master.as_raw_fd();

                    trace!("got pty: {slave_fd} {master_fd}");

                    // for stdin: write to master and read from slave
                    // for stdout/stderr: read from master and write to slave
                    stdin_pipe = (dup(slave_fd)?, dup(master_fd)?);
                    stdout_pipe = (dup(master_fd)?, dup(slave_fd)?);
                    stderr_pipe = (dup(master_fd)?, dup(slave_fd)?);
                }
                Some(ClientMessage::StartPayload(msg)) => {
                    wormhole_param = msg.wormhole_param;
                    break;
                }
                _ => {}
            }
        }

        if pty.is_none() {
            stdin_pipe = pipe()?;
            stdout_pipe = pipe()?;
            stderr_pipe = pipe()?;
        }
        let is_pty = pty.is_some();

        mount_setattr(
            None,
            "/mnt/wormhole-unified",
            libc::AT_RECURSIVE as u32,
            &MountAttr {
                attr_set: 0,
                attr_clr: 0,
                propagation: MS_PRIVATE,
                userns_fd: 0,
            },
        )?;
        let wormhole_mount = open_tree(
            "/mnt/wormhole-unified/nix",
            libc::OPEN_TREE_CLOEXEC | libc::OPEN_TREE_CLONE | libc::AT_RECURSIVE as u32,
        )?;

        let (_, log_pipe_write_fd) = pipe()?;
        let (exit_code_pipe_read_fd, exit_code_pipe_write_fd) = pipe()?;
        let wormhole_mount_fd = wormhole_mount.as_raw_fd();
        unset_cloexec(wormhole_mount_fd)?;
        unset_cloexec(exit_code_pipe_write_fd)?;
        unset_cloexec(log_pipe_write_fd)?;
        let mut exit_code_pipe_reader = AsyncFile::new(exit_code_pipe_read_fd)?;

        let mut config = serde_json::from_str::<WormholeConfig>(&wormhole_param)?;
        config.wormhole_mount_tree_fd = wormhole_mount_fd;
        config.log_fd = log_pipe_write_fd;
        config.exit_code_pipe_write_fd = exit_code_pipe_write_fd;

        trace!("spawning wormhole-attach");
        let _ = unsafe {
            Command::new("/wormhole-attach")
                .arg(serde_json::to_string(&config)?)
                .env("TERM", term_env.unwrap_or(String::from("")))
                .pre_exec(move || {
                    if pty.is_some() {
                        setsid()?;
                        let res = ioctl(stdin_pipe.0, TIOCSCTTY, 1);
                        if res != 0 {
                            return Err(std::io::Error::last_os_error());
                        }
                    }

                    dup2(stdin_pipe.0, libc::STDIN_FILENO)?;
                    dup2(stdout_pipe.1, libc::STDOUT_FILENO)?;
                    dup2(stderr_pipe.1, libc::STDERR_FILENO)?;

                    Ok(())
                })
                .spawn()?
        };

        let mut payload_stdin = AsyncFile::new(stdin_pipe.1)?;
        let mut payload_stdout = AsyncFile::new(stdout_pipe.0)?;
        let mut payload_stderr = AsyncFile::new(stderr_pipe.0)?;

        let cancel_token = CancellationToken::new();
        {
            let cancel_token = cancel_token.clone();
            let client_writer_m = Arc::clone(&client_writer_m);
            let _: JoinHandle<anyhow::Result<()>> = tokio::spawn(async move {
                trace!("starting reading payload stdout");
                let mut buf = [0u8; 1024];
                loop {
                    tokio::select! {
                        _ = cancel_token.cancelled() => {
                            trace!("cancelled stdout");
                            break;
                        }
                        result = payload_stdout.read(&mut buf) => {
                            match result {
                                Ok(0) => {
                                    trace!("got eof reading from payload stdout");
                                    break;
                                }
                                Ok(n) => {
                                    // trace!("writing");
                                    let mut client_writer = client_writer_m.lock().await;
                                    RpcServerMessage {
                                        server_message: Some(ServerMessage::StdoutData(StdoutData {
                                            data: buf[..n].to_vec(),
                                        })),
                                    }
                                    .write(&mut client_writer)
                                    .await?;
                                }
                                Err(e) => trace!("got error reading from payload stdout: {e}"),
                            }
                        }
                    }
                }
                trace!("finished reading payload stdout");
                Ok(())
            });
        }

        {
            let cancel_token = cancel_token.clone();
            let client_writer_m = Arc::clone(&client_writer_m);
            let _: JoinHandle<anyhow::Result<()>> = tokio::spawn(async move {
                let mut buf = [0u8; 1024];
                loop {
                    tokio::select! {
                        _ = cancel_token.cancelled() => {
                            trace!("cancelled stderr");
                            break;
                        }
                        result = payload_stderr.read(&mut buf) => {
                            match result {
                                Ok(0) => break,
                                Ok(n) => {
                                    let mut client_writer = client_writer_m.lock().await;
                                    RpcServerMessage {
                                        server_message: Some(ServerMessage::StderrData(StderrData {
                                            data: buf[..n].to_vec(),
                                        })),
                                    }
                                    .write(&mut client_writer)
                                    .await?;
                                }
                                Err(e) => trace!("got error reading from payload stderr: {e}"),
                            }
                        }
                    }
                }
                Ok(())
            });
        }

        {
            let cancel_token = cancel_token.clone();
            let _: JoinHandle<anyhow::Result<()>> = tokio::spawn(async move {
                loop {
                    tokio::select! {
                        _ = cancel_token.cancelled() => {
                            trace!("cancelled read");
                            break;
                        }
                        message = RpcClientMessage::read(&mut client_stdin) => {
                            match message?.client_message {
                                Some(ClientMessage::StdinData(msg)) => {
                                    // trace!("rpc: stdin data {:?}", String::from_utf8_lossy(&msg.data));
                                    let _ = payload_stdin
                                        .write(&msg.data)
                                        .await
                                        .map_err(|e| trace!("error writing to payload {e}"));
                                }
                                Some(ClientMessage::TerminalResize(msg)) => {
                                    if !is_pty {
                                        panic!("cannot resize terminal for non-tty ")
                                    }
                                    let ws = Winsize {
                                        ws_row: msg.rows as u16,
                                        ws_col: msg.cols as u16,
                                        ws_xpixel: 0,
                                        ws_ypixel: 0,
                                    };
                                    unsafe {
                                        nix::libc::ioctl(payload_stdin.as_raw_fd(), TIOCSWINSZ, &ws);
                                    }
                                }
                                _ => {}
                            }
                        }
                    }
                }
                Ok(())
            });
        }

        tokio::spawn(async move {
            let mut exit_code = [0u8];
            let _ = exit_code_pipe_reader.read_exact(&mut exit_code).await;

            trace!("received exit code {}", exit_code[0]);
            // sleep(Duration::from_secs(5)).await;

            cancel_token.cancel();
            // sleep(Duration::from_secs(1)).await;
            let mut client_writer = client_writer_m.lock().await;
            let _ = RpcServerMessage {
                server_message: Some(ServerMessage::ExitStatus(ExitStatus {
                    exit_code: exit_code[0] as u32,
                })),
            }
            .write(&mut client_writer)
            .await;

            // under flock: decrement connection refcount; if there are no more connections left, begin shutdown
            trace!("waiting for flock");
            let _flock = Flock::new_ofd(
                File::create("/data/.lock").unwrap(),
                FlockMode::Exclusive,
                FlockWait::Blocking,
            );

            trace!("waiting for lock");
            let mut lock = self.conn.lock().await;
            *lock -= 1;
            trace!("remaining connections: {}", *lock);
            if *lock == 0 {
                trace!("shutting down");
                let _ = std::fs::remove_file("/data/rpc.sock");
                let _ = unmount_wormhole();
                exit(0);
            }

            // todo: kill all async server tasks belonging to the current connection
        })
        .await?;
        trace!("exiting");

        // drop(stream);
        Ok(())
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    let server = WormholeServer::new();
    server.init()?;
    server.listen()?;

    Ok(())
}
