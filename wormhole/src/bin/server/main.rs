use anyhow::anyhow;
use libc::{TIOCSCTTY, TIOCSWINSZ};
use std::{
    fs::{self, OpenOptions},
    os::{
        fd::{AsRawFd, RawFd},
        unix::net::{UnixListener, UnixStream},
    },
    process::exit,
    sync::Arc,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    process::Command,
    sync::Mutex,
    task::JoinHandle,
};
use tokio_util::sync::CancellationToken;
use tracing_subscriber::fmt::format::FmtSpan;
use util::{mount_wormhole, unmount_wormhole, BUF_SIZE, UPPERDIR, WORKDIR, WORMHOLE_UNIFIED};
use wormhole::{
    asyncfile::AsyncFile,
    model::{WormholeConfig, WormholeRuntimeState},
    newmount::open_tree,
    rpc::{
        wormhole::{
            rpc_client_message::ClientMessage, rpc_server_message::ServerMessage, ClientConnectAck,
            ExitStatus, RpcClientMessage, RpcServerMessage, StartPayload, StderrData, StdoutData,
        },
        RpcRead, RpcWrite, RPC_SOCKET,
    },
    termios::create_pty,
    unset_cloexec,
};

use nix::{
    fcntl::{
        fcntl,
        FcntlArg::{self, F_GETFD},
        FdFlag, OFlag,
    },
    libc::ioctl,
    pty::{OpenptyResult, Winsize},
    sys::socket::{recvmsg, ControlMessageOwned, MsgFlags, RecvMsg},
    unistd::{dup, dup2, pipe2, setsid},
};
use tracing::{trace, Level};

mod util;

// receives client stdin and stdout fds with SCM_RIGHTS
fn recv_rpc_client(stream: &UnixStream) -> anyhow::Result<(AsyncFile, AsyncFile)> {
    let mut buf = [0u8; 1];
    let mut cmsgspace = nix::cmsg_space!([RawFd; 2]);

    let mut iov = [std::io::IoSliceMut::new(&mut buf)];
    let msg: RecvMsg<()> = recvmsg(
        stream.as_raw_fd(),
        &mut iov,
        Some(&mut cmsgspace),
        MsgFlags::MSG_CMSG_CLOEXEC,
    )?;

    let mut client_fds: Option<[RawFd; 2]> = None;
    for cmsg in msg.cmsgs() {
        if let ControlMessageOwned::ScmRights(fds) = cmsg {
            if fds.len() == 2 {
                client_fds = Some(fds.try_into().unwrap());
            }
        }
    }
    match client_fds {
        Some(fds) => {
            let client_stdin = AsyncFile::new(fds[0])?;
            let client_stdout = AsyncFile::new(fds[1])?;
            Ok((client_stdin, client_stdout))
        }
        _ => Err(anyhow!("did not get client stdin and stdout")),
    }
}

struct WormholeServer {
    count: Mutex<u32>,
}

impl WormholeServer {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            count: Mutex::new(0),
        })
    }

    fn init(&self) -> anyhow::Result<()> {
        trace!("initializing wormhole");
        mount_wormhole()
    }

    fn listen(self: &Arc<Self>) -> anyhow::Result<()> {
        let _ = std::fs::remove_file(RPC_SOCKET);
        let listener = UnixListener::bind(RPC_SOCKET)?;

        loop {
            match listener.accept() {
                Ok((stream, _)) => {
                    self.clone().spawn_client_handler(stream);
                }
                Err(e) => {
                    trace!("error accepting connection: {:?}", e);
                }
            }
        }
    }

    fn spawn_client_handler(self: Arc<Self>, stream: UnixStream) -> JoinHandle<()> {
        tokio::spawn(async move {
            {
                let mut lock = self.count.lock().await;
                *lock += 1;
                trace!("new connection: total {}", *lock);
            }
            match self.handle_client(stream).await {
                Ok(_) => {}
                Err(e) => {
                    trace!("error handling client: {:?}", e);
                }
            }
            {
                let mut lock = self.count.lock().await;
                *lock -= 1;
                trace!("remaining connections: {}", *lock);
                if *lock == 0 {
                    trace!("shutting down");
                    let _ = std::fs::remove_file(RPC_SOCKET);
                    let _ = unmount_wormhole();
                    exit(0);
                }
            }
        })
    }

    async fn reset_data(self: &Self, client_writer: Arc<Mutex<AsyncFile>>) -> anyhow::Result<()> {
        trace!("resetting data");
        let mut exit_code = 0;

        let lock = self.count.lock().await;
        if *lock == 1 {
            fs::remove_dir_all(UPPERDIR)?;
            fs::remove_dir_all(WORKDIR)?;
        } else {
            // other connections are present, so we cannot reset
            trace!("other connections present, cannot reset");
            exit_code = 1;
        }

        let mut client_writer = client_writer.lock().await;
        RpcServerMessage {
            server_message: Some(ServerMessage::ExitStatus(ExitStatus { exit_code })),
        }
        .write(&mut client_writer)
        .await?;

        Ok(())
    }

    fn spawn_client_to_payload(
        self: &Self,
        cancel_token: CancellationToken,
        mut client_stdin: AsyncFile,
        mut payload_stdin: AsyncFile,
        is_pty: bool,
    ) -> JoinHandle<()> {
        tokio::spawn(async move {
            loop {
                tokio::select! {
                    _ = cancel_token.cancelled() => {
                        return;
                    }
                    message = RpcClientMessage::read(&mut client_stdin) => {
                        // break early if we cannot read from client side
                        if message.is_err() {
                            cancel_token.cancel();
                            return;
                        }

                        match message.unwrap().client_message {
                            Some(ClientMessage::StdinData(msg)) => {
                                let _ = payload_stdin
                                    .write(&msg.data)
                                    .await
                                    .map_err(|e| trace!("error writing to payload {e}"));
                            }
                            Some(ClientMessage::TerminalResize(msg)) => {
                                if is_pty {
                                    let ws = Winsize {
                                        ws_row: msg.rows as u16,
                                        ws_col: msg.cols as u16,
                                        ws_xpixel: 0,
                                        ws_ypixel: 0,
                                    };
                                    let res = unsafe { ioctl(payload_stdin.as_raw_fd(), TIOCSWINSZ, &ws) };
                                    if res < 0 {
                                        trace!("error setting winsize: {res}");
                                    }
                                }
                            }
                            _ => {}
                        }
                    }
                }
            }
        })
    }

    fn spawn_payload_to_client(
        self: &Self,
        cancel_token: CancellationToken,
        client_writer: Arc<Mutex<AsyncFile>>,
        mut payload_output: AsyncFile,
        is_stdout: bool,
    ) -> JoinHandle<()> {
        tokio::spawn(async move {
            let mut buf = [0u8; BUF_SIZE];
            loop {
                tokio::select! {
                    _ = cancel_token.cancelled() => {
                        return;
                    }
                    result = payload_output.read(&mut buf) => {
                        match result {
                            Ok(0) => break,
                            Ok(n) => {
                                let mut client_writer = client_writer.lock().await;
                                let res = RpcServerMessage {
                                    server_message: Some(if is_stdout {
                                        ServerMessage::StdoutData(StdoutData {
                                            data: buf[..n].to_vec(),
                                        })
                                    } else {
                                        ServerMessage::StderrData(StderrData {
                                            data: buf[..n].to_vec(),
                                        })
                                    }),
                                }
                                .write(&mut client_writer)
                                .await;

                                // break early if we cannot write to client anymore
                                if res.is_err() {
                                    cancel_token.cancel();
                                    return;
                                }
                            }
                            Err(e) => trace!("got error reading from payload stdout: {e}"),
                        }
                    }
                }
            }
        })
    }

    async fn handle_client(self: &Self, mut stream: UnixStream) -> anyhow::Result<()> {
        // get client fds via scm_rights over the stream
        trace!("waiting for rpc client fds");
        let (mut client_stdin, mut client_stdout) = recv_rpc_client(&stream)?;
        trace!(
            "got rpc client fds {} {}",
            client_stdin.as_raw_fd(),
            client_stdout.as_raw_fd()
        );

        // Send ack to scon client. This is necessary so that the client knows the server refcount
        // was incremented and the server is not in the middle of exiting. If the client
        // receives an EOF before this ack, the client will assume the server was shutting down
        // and will attempt to create a new server.
        RpcServerMessage {
            server_message: Some(ServerMessage::ClientConnectAck(ClientConnectAck {})),
        }
        .write(&mut client_stdout)
        .await?;

        let client_writer = Arc::new(Mutex::new(client_stdout));

        loop {
            let message = RpcClientMessage::read(&mut client_stdin).await?;
            match message.client_message {
                Some(ClientMessage::StartPayload(msg)) => {
                    return self
                        .start_wormhole_attach(msg, client_stdin, client_writer)
                        .await;
                }

                Some(ClientMessage::ResetData(_)) => return self.reset_data(client_writer).await,
                _ => {}
            }
        }
    }

    async fn start_wormhole_attach(
        self: &Self,
        params: StartPayload,
        client_stdin: AsyncFile,
        client_writer: Arc<Mutex<AsyncFile>>,
    ) -> anyhow::Result<()> {
        let mut pty: Option<OpenptyResult> = None;
        let mut term_env: Option<String> = None;

        // (read, write) pipes
        let mut stdin_pipe: (RawFd, RawFd) = (-1, -1);
        let mut stdout_pipe: (RawFd, RawFd) = (-1, -1);
        let mut stderr_pipe: (RawFd, RawFd) = (-1, -1);

        if let Some(pty_config) = params.pty_config {
            pty = Some(create_pty(
                pty_config.cols as u16,
                pty_config.rows as u16,
                pty_config.termios,
            )?);
            term_env = Some(pty_config.term_env);

            let slave_fd = pty.as_ref().unwrap().slave.as_raw_fd();
            let master_fd = pty.as_ref().unwrap().master.as_raw_fd();

            trace!("created pty: {slave_fd} {master_fd}");

            // for stdin: write to master and read from slave
            stdin_pipe.0 = fcntl(slave_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stdin_pipe.1 = fcntl(master_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            // for stdout/stderr: read from master and write to slave
            stdout_pipe.0 = fcntl(master_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stdout_pipe.1 = fcntl(slave_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stderr_pipe.0 = fcntl(master_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stderr_pipe.1 = fcntl(slave_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            if stdin_pipe.0 < 0
                || stdin_pipe.1 < 0
                || stdout_pipe.0 < 0
                || stdout_pipe.1 < 0
                || stderr_pipe.0 < 0
                || stderr_pipe.1 < 0
            {
                return Err(anyhow!("failed to duplicate fds"));
            }
        }
        if pty.is_none() {
            stdin_pipe = pipe2(OFlag::O_CLOEXEC)?;
            stdout_pipe = pipe2(OFlag::O_CLOEXEC)?;
            stderr_pipe = pipe2(OFlag::O_CLOEXEC)?;
        }
        let is_pty = pty.is_some();

        let wormhole_mount = open_tree(
            &format!("{}/nix", WORMHOLE_UNIFIED),
            libc::OPEN_TREE_CLOEXEC | libc::OPEN_TREE_CLONE | libc::AT_RECURSIVE as u32,
        )?;

        let (_, log_pipe_write_fd) = pipe2(OFlag::O_CLOEXEC)?;
        let (exit_code_pipe_read_fd, exit_code_pipe_write_fd) = pipe2(OFlag::O_CLOEXEC)?;
        let wormhole_mount_fd = wormhole_mount.as_raw_fd();
        let mut exit_code_pipe_reader = AsyncFile::new(exit_code_pipe_read_fd)?;

        let config: WormholeConfig = serde_json::from_str(&params.wormhole_config)?;
        let runtime_state = WormholeRuntimeState {
            rootfs_fd: None,
            wormhole_mount_tree_fd: wormhole_mount_fd,
            log_fd: log_pipe_write_fd,
            exit_code_pipe_write_fd: exit_code_pipe_write_fd,
        };

        trace!("spawning wormhole-attach");
        let _ = unsafe {
            Command::new("/wormhole-attach")
                .args(&[
                    serde_json::to_string(&config)?,
                    serde_json::to_string(&runtime_state)?,
                ])
                .env("TERM", term_env.unwrap_or(String::from("")))
                .pre_exec(move || {
                    if is_pty {
                        setsid()?;
                        let res = ioctl(stdin_pipe.0, TIOCSCTTY, 1);
                        if res != 0 {
                            return Err(std::io::Error::last_os_error());
                        }
                    }

                    trace!("unsetting cloexec on fds for wormhole-attach");
                    unset_cloexec(wormhole_mount_fd)?;
                    unset_cloexec(exit_code_pipe_write_fd)?;
                    unset_cloexec(log_pipe_write_fd)?;

                    // no cloexec because wormhole-attach child should inherit stdio fds
                    dup2(stdin_pipe.0, libc::STDIN_FILENO)?;
                    dup2(stdout_pipe.1, libc::STDOUT_FILENO)?;
                    dup2(stderr_pipe.1, libc::STDERR_FILENO)?;

                    Ok(())
                })
                .spawn()?
        };

        let payload_stdin = AsyncFile::new(stdin_pipe.1)?;
        let payload_stdout = AsyncFile::new(stdout_pipe.0)?;
        let payload_stderr = AsyncFile::new(stderr_pipe.0)?;
        let cancel_token = CancellationToken::new();

        self.spawn_payload_to_client(
            cancel_token.clone(),
            client_writer.clone(),
            payload_stdout,
            true,
        );
        self.spawn_payload_to_client(
            cancel_token.clone(),
            client_writer.clone(),
            payload_stderr,
            false,
        );
        self.spawn_client_to_payload(cancel_token.clone(), client_stdin, payload_stdin, is_pty);

        let mut exit_code = [0u8];
        tokio::select! {
            _ = cancel_token.cancelled() => {
                trace!("cancelled read");
            }
            _ = exit_code_pipe_reader.read_exact(&mut exit_code) => {
                trace!("received exit code {}", exit_code[0]);
                cancel_token.cancel();
                let mut client_writer = client_writer.lock().await;
                let _ = RpcServerMessage {
                    server_message: Some(ServerMessage::ExitStatus(ExitStatus {
                        exit_code: exit_code[0] as u32,
                    })),
                }
                .write(&mut client_writer)
                .await;
            }
        }

        Ok(())
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let level = if cfg!(debug_assertions) {
        Level::TRACE
    } else {
        Level::INFO
    };
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(level)
        .init();

    trace!("starting wormhole server");
    let server = WormholeServer::new();
    server.init()?;
    server.listen()?;

    Ok(())
}
