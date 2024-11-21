use anyhow::anyhow;
use libc::TIOCSCTTY;
use nix::{
    fcntl::{fcntl, FcntlArg, OFlag},
    libc::ioctl,
    sys::socket::{recvmsg, ControlMessageOwned, MsgFlags, RecvMsg},
    unistd::{dup2, pipe2, setsid},
};
use std::{
    collections::HashMap,
    fs::{self},
    future::pending,
    os::{
        fd::{AsRawFd, FromRawFd, IntoRawFd, OwnedFd, RawFd},
        unix::net::{UnixListener, UnixStream},
    },
    sync::Arc,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    process::Command,
    task::{JoinHandle, JoinSet},
};
use tracing::{debug, info, trace, Level};
use tracing_subscriber::fmt::format::FmtSpan;
use util::{mount_wormhole, BUF_SIZE, UPPERDIR, WORKDIR, WORMHOLE_UNIFIED};
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
    set_nonblocking,
    termios::{create_pty, resize_pty},
    unset_cloexec,
};

mod util;

struct DropGuard {
    count: Arc<std::sync::Mutex<u32>>,
}

impl DropGuard {
    fn new(count: Arc<std::sync::Mutex<u32>>) -> Self {
        {
            let mut count = count.lock().unwrap();
            *count += 1;
            trace!("incremented, new refcount: {}", *count);
        }

        DropGuard { count }
    }
}

impl Drop for DropGuard {
    fn drop(&mut self) {
        let mut count = self.count.lock().unwrap();
        *count -= 1;
        trace!("decremented, new refcount: {}", *count);

        if *count == 0 {
            debug!("shutting down");

            match util::shutdown() {
                Ok(_) => debug!("shutdown complete"),
                Err(e) => debug!("error shutting down: {:?}", e),
            }
        }
    }
}

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
            set_nonblocking(fds[0])?;
            set_nonblocking(fds[1])?;
            let client_stdin = AsyncFile::new(fds[0])?;
            let client_stdout = AsyncFile::new(fds[1])?;
            Ok((client_stdin, client_stdout))
        }
        _ => Err(anyhow!("did not get client stdin and stdout")),
    }
}

struct WormholeServer {
    // store the count as std::sync::Mutex since Drop cannot be async. This is fine because
    // we don't need to ever hold the lock across any await points.
    count: Arc<std::sync::Mutex<u32>>,
}

impl WormholeServer {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            // note: we also wrap the count in an Arc so that we can share the count mutex
            // with the tokio task that spawns wormhole-attach and handles refcount decrement -
            // otherwise, the wormhole-attach task may outlive its reference to count
            count: Arc::new(std::sync::Mutex::new(0)),
        })
    }

    fn init(&self) -> anyhow::Result<()> {
        debug!("initializing wormhole");
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
                    debug!("error accepting connection: {:?}", e);
                }
            }
        }
    }

    fn spawn_client_handler(self: Arc<Self>, stream: UnixStream) -> JoinHandle<()> {
        tokio::spawn(async move {
            match self.handle_client(stream).await {
                Ok(_) => {}
                Err(e) => {
                    debug!("error handling client: {:?}", e);
                }
            }
        })
    }

    async fn forward_client_to_payload(
        mut client_stdin: AsyncFile,
        payload_stdin: AsyncFile,
        pty_master_fd: OwnedFd,
        is_pty: bool,
    ) -> anyhow::Result<()> {
        let mut payload_stdin = Some(payload_stdin);
        loop {
            // return early and abort all tasks if the client has disconnected
            let message = RpcClientMessage::read(&mut client_stdin).await?;
            match message.client_message {
                Some(ClientMessage::StdinData(msg)) => {
                    // zero-byte message indicates client stdin EOF; drop the payload stdin
                    // to forward the EOF to wormhole-attach process
                    if msg.data.len() == 0 {
                        payload_stdin = None;
                        debug!("client stdin EOF, dropping payload stdin");
                    }

                    if let Some(payload_stdin) = payload_stdin.as_mut() {
                        if let Err(e) = payload_stdin.write_all(&msg.data).await {
                            debug!("got error writing to payload stdin: {e}");
                        }
                    }
                }
                Some(ClientMessage::TerminalResize(msg)) => {
                    debug!("terminal resize: {} {}", msg.rows, msg.cols);
                    if is_pty {
                        if let Err(e) = resize_pty(&pty_master_fd, msg.rows as u16, msg.cols as u16)
                        {
                            debug!("got error resizing pty: {e}");
                        }
                    }
                }
                // todo: add support for forwarding signals
                _ => {
                    debug!("unknown message from client");
                }
            }
        }
    }

    async fn forward_payload_to_client(
        client_writer: Arc<tokio::sync::Mutex<AsyncFile>>,
        mut payload_output: AsyncFile,
        is_stdout: bool,
    ) -> anyhow::Result<()> {
        let mut buf = [0u8; BUF_SIZE];
        let stdio_name = if is_stdout { "stdout" } else { "stderr" };
        loop {
            match payload_output.read(&mut buf).await {
                Ok(0) => {
                    // see comments further below; only abort tasks if client disconnects,
                    // otherwise wait indefinitely until the exit code task finishes
                    debug!("payload {} closed", stdio_name);
                    pending().await
                }
                Ok(n) => {
                    let server_message = if is_stdout {
                        ServerMessage::StdoutData(StdoutData {
                            data: buf[..n].to_vec(),
                        })
                    } else {
                        ServerMessage::StderrData(StderrData {
                            data: buf[..n].to_vec(),
                        })
                    };

                    // return early and abort all tasks if we cannot write to the client
                    let mut client_writer = client_writer.lock().await;
                    RpcServerMessage {
                        server_message: Some(server_message),
                    }
                    .write_all(&mut client_writer)
                    .await?;
                }
                Err(e) => {
                    debug!("got error reading from payload {stdio_name}: {e}");
                }
            }
        }
    }

    async fn forward_exit_code(
        mut exit_code_pipe_reader: AsyncFile,
        client_writer: Arc<tokio::sync::Mutex<AsyncFile>>,
    ) -> anyhow::Result<()> {
        let mut exit_code = [0u8];
        exit_code_pipe_reader.read_exact(&mut exit_code).await?;
        debug!("received exit code {}", exit_code[0]);

        let mut client_writer = client_writer.lock().await;
        let _ = RpcServerMessage {
            server_message: Some(ServerMessage::ExitStatus(ExitStatus {
                exit_code: exit_code[0] as u32,
            })),
        }
        .write_all(&mut client_writer)
        .await?;

        Ok(())
    }

    async fn handle_client(&self, stream: UnixStream) -> anyhow::Result<()> {
        // get client fds via scm_rights over the stream
        debug!("waiting for rpc client fds");
        let (mut client_stdin, mut client_stdout) = recv_rpc_client(&stream)?;
        debug!(
            "got rpc client fds {} {}",
            client_stdin.as_raw_fd(),
            client_stdout.as_raw_fd()
        );
        // increment connection refcount through guard and pass ownership to the corresponding handler
        let guard = DropGuard::new(self.count.clone());

        // Send ack to scon client. This is necessary so that the client knows the server refcount
        // was incremented and the server is not in the middle of exiting. If the client
        // receives an EOF before this ack, the client will assume the server was shutting down
        // and will attempt to create a new server.
        RpcServerMessage {
            server_message: Some(ServerMessage::ClientConnectAck(ClientConnectAck {})),
        }
        .write_all(&mut client_stdout)
        .await?;

        let client_writer = Arc::new(tokio::sync::Mutex::new(client_stdout));

        loop {
            let message = RpcClientMessage::read(&mut client_stdin).await?;
            match message.client_message {
                Some(ClientMessage::StartPayload(msg)) => {
                    return self
                        .run_debug_session(msg, client_stdin, client_writer, guard)
                        .await;
                }

                Some(ClientMessage::ResetData(_)) => {
                    return self.reset_data(client_writer, guard).await
                }
                _ => {}
            }
        }
    }

    async fn run_debug_session(
        &self,
        params: StartPayload,
        client_stdin: AsyncFile,
        client_writer: Arc<tokio::sync::Mutex<AsyncFile>>,
        guard: DropGuard,
    ) -> anyhow::Result<()> {
        info!("starting debug session");
        let mut env: HashMap<String, String> = HashMap::new();

        // (read, write) pipes
        let stdin_pipe: (OwnedFd, OwnedFd);
        let stdout_pipe: (OwnedFd, OwnedFd);
        let stderr_pipe: (OwnedFd, OwnedFd);
        let is_pty = params.pty_config.is_some();

        if let Some(pty_config) = params.pty_config {
            env.insert("TERM".to_string(), pty_config.term_env);
            env.insert("SSH_CONNECTION".to_string(), pty_config.ssh_connection_env);
            env.insert("SSH_AUTH_SOCK".to_string(), pty_config.ssh_auth_sock_env);

            let (master_fd, slave_fd) = create_pty(
                pty_config.rows as u16,
                pty_config.cols as u16,
                pty_config.termios,
            )?;
            debug!(
                "created pty: {:?} {:?}",
                master_fd.as_raw_fd(),
                slave_fd.as_raw_fd()
            );

            // for stdin: write to master and read from slave
            stdin_pipe = (
                unsafe {
                    OwnedFd::from_raw_fd(fcntl(slave_fd.as_raw_fd(), FcntlArg::F_DUPFD_CLOEXEC(3))?)
                },
                unsafe {
                    OwnedFd::from_raw_fd(fcntl(
                        master_fd.as_raw_fd(),
                        FcntlArg::F_DUPFD_CLOEXEC(3),
                    )?)
                },
            );
            // for stdout/stderr: read from master and write to slave
            stdout_pipe = (
                unsafe {
                    OwnedFd::from_raw_fd(fcntl(
                        master_fd.as_raw_fd(),
                        FcntlArg::F_DUPFD_CLOEXEC(3),
                    )?)
                },
                unsafe {
                    OwnedFd::from_raw_fd(fcntl(slave_fd.as_raw_fd(), FcntlArg::F_DUPFD_CLOEXEC(3))?)
                },
            );
            // reuse original master and slave fds to save a dup call
            stderr_pipe = (master_fd, slave_fd);
        } else {
            let stdin_pipe_fds = pipe2(OFlag::O_CLOEXEC)?;
            let stdout_pipe_fds = pipe2(OFlag::O_CLOEXEC)?;
            let stderr_pipe_fds = pipe2(OFlag::O_CLOEXEC)?;

            stdin_pipe = (unsafe { OwnedFd::from_raw_fd(stdin_pipe_fds.0) }, unsafe {
                OwnedFd::from_raw_fd(stdin_pipe_fds.1)
            });
            stdout_pipe = (unsafe { OwnedFd::from_raw_fd(stdout_pipe_fds.0) }, unsafe {
                OwnedFd::from_raw_fd(stdout_pipe_fds.1)
            });
            stderr_pipe = (unsafe { OwnedFd::from_raw_fd(stderr_pipe_fds.0) }, unsafe {
                OwnedFd::from_raw_fd(stderr_pipe_fds.1)
            });
        }

        let wormhole_mount = open_tree(
            &format!("{}/nix", WORMHOLE_UNIFIED),
            libc::OPEN_TREE_CLOEXEC | libc::OPEN_TREE_CLONE | libc::AT_RECURSIVE as u32,
        )?;

        let (_, log_pipe_write_fd) = pipe2(OFlag::O_CLOEXEC)?;
        let (exit_code_pipe_read_fd, exit_code_pipe_write_fd) = pipe2(OFlag::O_CLOEXEC)?;
        let wormhole_mount_fd = wormhole_mount.as_raw_fd();
        set_nonblocking(exit_code_pipe_read_fd)?;
        let exit_code_pipe_reader = AsyncFile::new(exit_code_pipe_read_fd)?;

        let config: WormholeConfig = serde_json::from_str(&params.wormhole_config)?;
        // todo: rootfs support for stopped containers / images
        let runtime_state = WormholeRuntimeState {
            rootfs_fd: None,
            wormhole_mount_tree_fd: wormhole_mount_fd,
            exit_code_pipe_write_fd: exit_code_pipe_write_fd,
            log_fd: log_pipe_write_fd,
        };

        debug!("spawning wormhole-attach");

        let mut child = unsafe {
            Command::new("/bin/wormhole-attach")
                .args(&[
                    serde_json::to_string(&config)?,
                    serde_json::to_string(&runtime_state)?,
                ])
                .envs(env)
                .pre_exec(move || {
                    if is_pty {
                        setsid()?;
                        let res = ioctl(stdin_pipe.0.as_raw_fd(), TIOCSCTTY, 1);
                        if res != 0 {
                            return Err(std::io::Error::last_os_error());
                        }
                    }

                    debug!("unsetting cloexec on fds for wormhole-attach");
                    unset_cloexec(wormhole_mount_fd)?;
                    unset_cloexec(exit_code_pipe_write_fd)?;
                    unset_cloexec(log_pipe_write_fd)?;

                    // no dup cloexec because wormhole-attach child should inherit stdio fds
                    dup2(stdin_pipe.0.as_raw_fd(), libc::STDIN_FILENO)?;
                    dup2(stdout_pipe.1.as_raw_fd(), libc::STDOUT_FILENO)?;
                    dup2(stderr_pipe.1.as_raw_fd(), libc::STDERR_FILENO)?;

                    Ok(())
                })
                .spawn()?
        };

        tokio::spawn(async move {
            match child.wait().await {
                Ok(_) => {
                    debug!("wormhole-attach finished");
                }
                Err(e) => {
                    debug!("wormhole-attach failed with error: {:?}", e);
                }
            }

            // todo: decrement refcount once wormhole-attach exits, which ensures all background tasks
            // are finished
            debug!("wormhole-attach finished, decrementing refcount");
            drop(guard);
        });

        set_nonblocking(stdin_pipe.1.as_raw_fd())?;
        set_nonblocking(stdout_pipe.0.as_raw_fd())?;
        set_nonblocking(stderr_pipe.0.as_raw_fd())?;
        let payload_stdin = AsyncFile::new(stdin_pipe.1.into_raw_fd())?;
        let payload_stdout = AsyncFile::new(stdout_pipe.0.into_raw_fd())?;
        let payload_stderr = AsyncFile::new(stderr_pipe.0.into_raw_fd())?;

        let mut join_set = JoinSet::new();
        join_set.spawn(Self::forward_payload_to_client(
            client_writer.clone(),
            payload_stdout.try_clone()?,
            true,
        ));
        join_set.spawn(Self::forward_payload_to_client(
            client_writer.clone(),
            payload_stderr,
            false,
        ));
        join_set.spawn(Self::forward_client_to_payload(
            client_stdin,
            payload_stdin,
            // use payload stdout as the master pty fd for the sake of terminal resize, in case
            // we close the stdin pipe
            OwnedFd::from(payload_stdout),
            is_pty,
        ));
        join_set.spawn(Self::forward_exit_code(
            exit_code_pipe_reader,
            client_writer.clone(),
        ));

        // we should only abort all tasks if either:
        // - 1. the client disconnects (i.e. reading stdin from client or writing stdout/stderr
        // to client fails), because then there's no need to communicate stdio and exit code
        // - 2. the exit code task finishes
        //
        // note: if any payload stdio read/write operations fail, we block indefinitely rather
        // than return early and abort all tasks
        if let Some(res) = join_set.join_next().await {
            // res is either Ok (task completed succesfully) or Err (task panicked or aborted)
            match res {
                Ok(res) => debug!("task completed: {:?}", res),
                Err(e) => debug!("task panicked or aborted: {:?}", e),
            }

            debug!("shutting down connection tasks");
            join_set.shutdown().await;
        }

        // note: this does not necessarily mean the wormhole-attach has finished (due to background tasks)
        debug!("rpc communication finished");
        Ok(())
    }

    async fn reset_data(
        &self,
        client_writer: Arc<tokio::sync::Mutex<AsyncFile>>,
        _guard: DropGuard,
    ) -> anyhow::Result<()> {
        info!("resetting data");
        let mut exit_code = 0;

        {
            let lock = self.count.lock().unwrap();
            if *lock == 1 {
                fs::remove_dir_all(UPPERDIR)?;
                fs::remove_dir_all(WORKDIR)?;
            } else {
                // other connections are present, so we cannot reset
                debug!("other connections present, cannot reset");
                exit_code = 1;
            }
        }

        let mut client_writer = client_writer.lock().await;
        RpcServerMessage {
            server_message: Some(ServerMessage::ExitStatus(ExitStatus { exit_code })),
        }
        .write_all(&mut client_writer)
        .await?;

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

    debug!("starting wormhole server");
    let server = WormholeServer::new();
    server.init()?;
    server.listen()?;

    Ok(())
}
