use anyhow::anyhow;
use libc::{TIOCSCTTY, TIOCSWINSZ};
use std::{
    collections::HashMap,
    fs::{self, OpenOptions},
    os::{
        fd::{AsRawFd, FromRawFd, IntoRawFd, OwnedFd, RawFd},
        unix::net::{UnixListener, UnixStream},
    },
    process::exit,
    sync::Arc,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    process::Command,
    sync::Mutex,
    task::{JoinHandle, JoinSet},
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
    unistd::{close, dup, dup2, pipe2, setsid},
};
use tracing::{debug, info, trace, Level};

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
            {
                let mut lock = self.count.lock().await;
                *lock += 1;
                trace!("new connection: total {}", *lock);
            }
            match self.handle_client(stream).await {
                Ok(_) => {}
                Err(e) => {
                    debug!("error handling client: {:?}", e);
                }
            }
            {
                let mut lock = self.count.lock().await;
                *lock -= 1;
                trace!("remaining connections: {}", *lock);
                if *lock == 0 {
                    debug!("shutting down");

                    let _ = std::fs::remove_file(RPC_SOCKET);
                    // tokio::time::sleep(tokio::time::Duration::from_secs(5)).await;
                    match unmount_wormhole() {
                        Ok(_) => {}
                        Err(e) => debug!("error unmounting wormhole: {:?}", e),
                    }
                    exit(0);
                }
            }
        })
    }

    async fn reset_data(self: &Self, client_writer: Arc<Mutex<AsyncFile>>) -> anyhow::Result<()> {
        info!("resetting data");
        let mut exit_code = 0;

        let lock = self.count.lock().await;
        if *lock == 1 {
            fs::remove_dir_all(UPPERDIR)?;
            fs::remove_dir_all(WORKDIR)?;
        } else {
            // other connections are present, so we cannot reset
            debug!("other connections present, cannot reset");
            exit_code = 1;
        }

        let mut client_writer = client_writer.lock().await;
        RpcServerMessage {
            server_message: Some(ServerMessage::ExitStatus(ExitStatus { exit_code })),
        }
        .write(&mut client_writer)
        .await?;
        // drop(lock);
        // tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;

        Ok(())
    }

    async fn forward_client_to_payload(
        mut client_stdin: AsyncFile,
        mut payload_stdin: AsyncFile,
        is_pty: bool,
    ) -> anyhow::Result<()> {
        loop {
            let message = RpcClientMessage::read(&mut client_stdin).await?;
            match message.client_message {
                Some(ClientMessage::StdinData(msg)) => {
                    let n = payload_stdin.write(&msg.data).await?;
                    if n == 0 {
                        return Err(anyhow!("payload stdin closed"));
                    }
                }
                Some(ClientMessage::TerminalResize(msg)) => {
                    debug!("terminal resize: {} {}", msg.rows, msg.cols);
                    if is_pty {
                        let ws = Winsize {
                            ws_row: msg.rows as u16,
                            ws_col: msg.cols as u16,
                            ws_xpixel: 0,
                            ws_ypixel: 0,
                        };
                        let res = unsafe { ioctl(payload_stdin.as_raw_fd(), TIOCSWINSZ, &ws) };
                        if res < 0 {
                            return Err(anyhow!("error setting winsize: {res}"));
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
        client_writer: Arc<Mutex<AsyncFile>>,
        mut payload_output: AsyncFile,
        is_stdout: bool,
    ) -> anyhow::Result<()> {
        let mut buf = [0u8; BUF_SIZE];
        loop {
            match payload_output.read(&mut buf).await {
                Ok(0) => return Err(anyhow!("stdout closed")),
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

                    // propagate errors (return early) if we cannot write to client anymore
                    let mut client_writer = client_writer.lock().await;
                    RpcServerMessage {
                        server_message: Some(server_message),
                    }
                    .write(&mut client_writer)
                    .await?;
                }
                Err(e) => return Err(anyhow!("got error reading from payload: {e}")),
            }
        }
    }

    async fn forward_exit_code(
        mut exit_code_pipe_reader: AsyncFile,
        client_writer: Arc<Mutex<AsyncFile>>,
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
        .write(&mut client_writer)
        .await?;

        Ok(())
    }

    async fn handle_client(self: &Self, stream: UnixStream) -> anyhow::Result<()> {
        // get client fds via scm_rights over the stream
        debug!("waiting for rpc client fds");
        let (mut client_stdin, mut client_stdout) = recv_rpc_client(&stream)?;
        debug!(
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
                        .run_debug_session(msg, client_stdin, client_writer)
                        .await;
                }

                Some(ClientMessage::ResetData(_)) => return self.reset_data(client_writer).await,
                _ => {}
            }
        }
    }

    async fn run_debug_session(
        self: &Self,
        params: StartPayload,
        client_stdin: AsyncFile,
        client_writer: Arc<Mutex<AsyncFile>>,
    ) -> anyhow::Result<()> {
        info!("starting wormhole-attach");
        let mut pty: Option<OpenptyResult> = None;
        let mut env: HashMap<String, String> = HashMap::new();

        // (read, write) pipes
        let mut stdin_pipe_fds: (RawFd, RawFd) = (-1, -1);
        let mut stdout_pipe_fds: (RawFd, RawFd) = (-1, -1);
        let mut stderr_pipe_fds: (RawFd, RawFd) = (-1, -1);
        let is_pty = params.pty_config.is_some();

        if let Some(pty_config) = params.pty_config {
            pty = Some(create_pty(
                pty_config.cols as u16,
                pty_config.rows as u16,
                pty_config.termios,
            )?);
            env.insert("TERM".to_string(), pty_config.term_env);
            env.insert("SSH_CONNECTION".to_string(), pty_config.ssh_connection_env);
            env.insert("SSH_AUTH_SOCK".to_string(), pty_config.ssh_auth_sock_env);

            let slave_fd = pty.as_ref().unwrap().slave.as_raw_fd();
            let master_fd = pty.as_ref().unwrap().master.as_raw_fd();

            debug!("created pty: {slave_fd} {master_fd}");

            // for stdin: write to master and read from slave
            stdin_pipe_fds.0 = fcntl(slave_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stdin_pipe_fds.1 = fcntl(master_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            // for stdout/stderr: read from master and write to slave
            stdout_pipe_fds.0 = fcntl(master_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stdout_pipe_fds.1 = fcntl(slave_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stderr_pipe_fds.0 = fcntl(master_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;
            stderr_pipe_fds.1 = fcntl(slave_fd, FcntlArg::F_DUPFD_CLOEXEC(3))?;

            // drop the original master and slave fds so that there are no additional references
            // to stdin/stdout/stderr pipes
            drop(pty);
        } else {
            stdin_pipe_fds = pipe2(OFlag::O_CLOEXEC)?;
            stdout_pipe_fds = pipe2(OFlag::O_CLOEXEC)?;
            stderr_pipe_fds = pipe2(OFlag::O_CLOEXEC)?;
        }

        // we can safely own these fds because we created them via dup/pipe2 above
        let stdin_pipe = (unsafe { OwnedFd::from_raw_fd(stdin_pipe_fds.0) }, unsafe {
            OwnedFd::from_raw_fd(stdin_pipe_fds.1)
        });
        let stdout_pipe = (unsafe { OwnedFd::from_raw_fd(stdout_pipe_fds.0) }, unsafe {
            OwnedFd::from_raw_fd(stdout_pipe_fds.1)
        });
        let stderr_pipe = (unsafe { OwnedFd::from_raw_fd(stderr_pipe_fds.0) }, unsafe {
            OwnedFd::from_raw_fd(stderr_pipe_fds.1)
        });

        let wormhole_mount = open_tree(
            &format!("{}/nix", WORMHOLE_UNIFIED),
            libc::OPEN_TREE_CLOEXEC | libc::OPEN_TREE_CLONE | libc::AT_RECURSIVE as u32,
        )?;

        let (_, log_pipe_write_fd) = pipe2(OFlag::O_CLOEXEC)?;
        let (exit_code_pipe_read_fd, exit_code_pipe_write_fd) = pipe2(OFlag::O_CLOEXEC)?;
        let wormhole_mount_fd = wormhole_mount.as_raw_fd();
        let exit_code_pipe_reader = AsyncFile::new(exit_code_pipe_read_fd)?;

        let config: WormholeConfig = serde_json::from_str(&params.wormhole_config)?;
        // todo: rootfs support for stopped containers / images
        let runtime_state = WormholeRuntimeState {
            rootfs_fd: None,
            wormhole_mount_tree_fd: wormhole_mount_fd,
            log_fd: log_pipe_write_fd,
            exit_code_pipe_write_fd: exit_code_pipe_write_fd,
        };

        debug!("spawning wormhole-attach");

        let mut child = unsafe {
            Command::new("/wormhole-attach")
                .args(&[
                    serde_json::to_string(&config)?,
                    serde_json::to_string(&runtime_state)?,
                ])
                .envs(env)
                .pre_exec(move || {
                    if is_pty {
                        setsid()?;
                        let res = ioctl(stdin_pipe_fds.0, TIOCSCTTY, 1);
                        if res != 0 {
                            return Err(std::io::Error::last_os_error());
                        }
                    }

                    debug!("unsetting cloexec on fds for wormhole-attach");
                    unset_cloexec(wormhole_mount_fd)?;
                    unset_cloexec(exit_code_pipe_write_fd)?;
                    unset_cloexec(log_pipe_write_fd)?;

                    // no dup cloexec because wormhole-attach child should inherit stdio fds
                    dup2(stdin_pipe_fds.0, libc::STDIN_FILENO)?;
                    dup2(stdout_pipe_fds.1, libc::STDOUT_FILENO)?;
                    dup2(stderr_pipe_fds.1, libc::STDERR_FILENO)?;

                    Ok(())
                })
                .spawn()?
        };

        // close child stdio pipe ends
        drop(stdin_pipe.0);
        drop(stdout_pipe.1);
        drop(stderr_pipe.1);

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
        });

        let payload_stdin = AsyncFile::new(stdin_pipe.1.into_raw_fd())?;
        let payload_stdout = AsyncFile::new(stdout_pipe.0.into_raw_fd())?;
        let payload_stderr = AsyncFile::new(stderr_pipe.0.into_raw_fd())?;

        let mut join_set = JoinSet::new();
        join_set.spawn(Self::forward_payload_to_client(
            client_writer.clone(),
            payload_stdout,
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
            is_pty,
        ));
        join_set.spawn(Self::forward_exit_code(
            exit_code_pipe_reader,
            client_writer.clone(),
        ));

        if let Some(res) = join_set.join_next().await {
            // res is either Ok (task completed succesfully) or Err (task panicked or aborted)
            match res {
                Ok(res) => debug!("server task completed: {:?}", res),
                Err(e) => debug!("server task panicked or aborted: {:?}", e),
            }

            // abort all tasks once any one of the tasks end
            debug!("shutting down remaining connection tasks");
            join_set.shutdown().await;
        }

        debug!("wormhole-attach finished");
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
