use anyhow::anyhow;
use libc::{winsize, TIOCSCTTY, TIOCSWINSZ};
use std::{
    ffi::CString,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd, RawFd},
        unix::net::{UnixListener, UnixStream},
    },
    path::Path,
    sync::Arc,
    thread,
};
use tokio::{
    io::{self, AsyncReadExt, AsyncWriteExt},
    process::Command,
    sync::Mutex,
    task::{self, JoinHandle},
};
use tracing_subscriber::fmt::format::FmtSpan;
use wormhole::{
    asyncfile::AsyncFile,
    flock::{Flock, FlockMode, FlockWait},
    newmount::open_tree,
    rpc::{RpcInputMessage, RpcOutputMessage},
    unset_cloexec,
};

use nix::{
    libc::ioctl,
    mount::{mount, MsFlags},
    pty::{OpenptyResult, Winsize},
    sys::socket::{recvmsg, ControlMessageOwned, MsgFlags},
    unistd::{dup, dup2, pipe, setsid, sleep},
};
use tracing::{trace, Level};

mod model;
use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    // renamed for obfuscation, as this may be user-visible
    #[serde(rename = "a")]
    pub init_pid: i32,
    #[serde(rename = "b", default)]
    pub wormhole_mount_tree_fd: RawFd,
    #[serde(rename = "c", default)]
    pub exit_code_pipe_write_fd: RawFd,
    #[serde(rename = "d", default)]
    pub log_fd: RawFd,
    #[serde(rename = "e")]
    pub drm_token: String,

    #[serde(rename = "f")]
    pub container_workdir: Option<String>,
    #[serde(rename = "g")]
    pub container_env: Option<Vec<String>>,

    #[serde(rename = "h")]
    pub entry_shell_cmd: Option<String>,

    #[serde(rename = "i")]
    pub is_local: bool,
}

const ROOTFS: &str = "/wormhole-rootfs";
const UPPERDIR: &str = "/data/upper";
const WORKDIR: &str = "/data/work";
const WORMHOLE_OVERLAY: &str = "/mnt/wormhole-overlay";
const WORMHOLE_UNIFIED: &str = "/mnt/wormhole-unified";
const REFCOUNT_FILE: &str = "/data/refcount";
const REFCOUNT_LOCK: &str = "/data/refcount.lock";

const NIX_RW_DIRS: [&str; 3] = ["store", "var", "orb/data"];

// receives client stdin and stdout fds  with SCM_RIGHTS
fn recv_rpc_client(stream: &UnixStream) -> anyhow::Result<(File, File)> {
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
            trace!("got rpc client: {stdin_fd}, {stdout_fd}");
            let client_stdin = unsafe { File::from_raw_fd(stdin_fd) };
            let client_stdout = unsafe { File::from_raw_fd(stdout_fd) };
            Ok((client_stdin, client_stdout))
        }
        _ => Err(anyhow!("did not get client stdin and stdout")),
    }
}

fn mount_wormhole() -> anyhow::Result<()> {
    // create upper, work, and overlay if they do not exist
    fs::create_dir_all(UPPERDIR)?;
    fs::create_dir_all(WORKDIR)?;
    fs::create_dir_all(WORMHOLE_OVERLAY)?;

    trace!("mounting overlayfs");
    let options = format!(
        "lowerdir={},upperdir={},workdir={}",
        ROOTFS, UPPERDIR, WORKDIR
    );
    mount(
        Some("overlay"),
        WORMHOLE_OVERLAY,
        Some("overlay"),
        MsFlags::empty(),
        Some(options.as_str()),
    )?;

    trace!("creating ro wormhole-unified mount");
    mount::<str, str, Path, Path>(Some(ROOTFS), WORMHOLE_UNIFIED, None, MsFlags::MS_BIND, None)?;
    mount::<str, str, Path, Path>(
        Some(ROOTFS),
        WORMHOLE_UNIFIED,
        None,
        MsFlags::MS_BIND | MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY,
        None,
    )?;

    for nix_dir in NIX_RW_DIRS {
        trace!("mount bind from overlay to unified: {}", nix_dir);
        mount::<str, str, Path, Path>(
            Some(format!("{}/nix/{}", WORMHOLE_OVERLAY, nix_dir).as_str()),
            format!("{}/nix/{}", WORMHOLE_UNIFIED, nix_dir).as_str(),
            None,
            MsFlags::MS_BIND,
            None,
        )?;
    }
    Ok(())
}

fn startup() -> anyhow::Result<()> {
    trace!("startup");
    OpenOptions::new()
        .create(true)
        .write(true)
        .append(true)
        .open(REFCOUNT_FILE)?;
    let _flock = Flock::new_ofd(
        File::create(REFCOUNT_LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;
    let contents = fs::read_to_string(REFCOUNT_FILE)?;
    let mut refcount: i32 = if contents.is_empty() {
        0
    } else {
        contents.trim().parse()?
    };

    // sometimes the refcount is non-zero even though the nix directory is not mounted. this can
    // happen when a wormhole session increments the refcount, but is killed before it has
    // the chance to decrement.
    if refcount == 0 || !Path::new(&format!("{WORMHOLE_UNIFIED}/nix")).exists() {
        mount_wormhole()?;
        refcount = 0;
    }

    refcount += 1;
    trace!("updated refcount from {} to {}", refcount - 1, refcount);

    fs::write(REFCOUNT_FILE, refcount.to_string())?;
    Ok(())
}

async fn handle_client(stream: UnixStream) -> anyhow::Result<()> {
    trace!("waiting for rpc client fds");
    // get client fds via scm_rights over the stream
    let (mut client_stdin, mut client_stdout) = recv_rpc_client(&stream)?;
    trace!(
        "got rpc client fds {} {}",
        client_stdin.as_raw_fd(),
        client_stdout.as_raw_fd()
    );

    // todo: increment connection count

    // todo: let user set this via rpc

    let mut client_stdin = AsyncFile::from(client_stdin)?;
    let mut client_stdout = AsyncFile::from(client_stdout)?;

    // acknowledge
    let _ = RpcOutputMessage::ConnectServerAck()
        .write_to(&mut client_stdout)
        .await;

    let mut client_writer_m = Arc::new(Mutex::new(client_stdout));
    let mut pty: Option<OpenptyResult> = None;
    let wormhole_param;

    let mut stdin_pipe: (RawFd, RawFd) = (-1, -1);
    let mut stdout_pipe: (RawFd, RawFd) = (-1, -1);
    let mut stderr_pipe: (RawFd, RawFd) = (-1, -1);

    // wait until user calls start before proceeding
    loop {
        match RpcInputMessage::read_from(&mut client_stdin).await {
            Ok(RpcInputMessage::RequestPty(pty_config)) => {
                pty = Some(pty_config.pty);
                let slave_fd = pty.as_ref().unwrap().slave.as_raw_fd();
                let master_fd = pty.as_ref().unwrap().master.as_raw_fd();

                trace!("got pty: {slave_fd} {master_fd}");

                // give each pipe ownership over its own master and slave so that it can drop them
                // for stdin: write to master and read from slave
                // for stdout/stderr: read from master and write to slave
                stdin_pipe = (dup(slave_fd)?, dup(master_fd)?);
                stdout_pipe = (dup(master_fd)?, dup(slave_fd)?);
                stderr_pipe = (dup(master_fd)?, dup(slave_fd)?);

                // cstr_envs.push(CString::new(format!("TERM={}", pty_config.term_env))?);
            }
            Ok(RpcInputMessage::StartPayload(params)) => {
                wormhole_param = params;
                break;
            }
            _ => {}
        };
    }

    if pty.is_none() {
        stdin_pipe = pipe()?;
        stdout_pipe = pipe()?;
        stderr_pipe = pipe()?;
    }
    let is_pty = pty.is_some();

    let wormhole_mount = open_tree(
        "/mnt/wormhole-unified/nix",
        libc::OPEN_TREE_CLOEXEC as i32 | libc::OPEN_TREE_CLONE as i32 | libc::AT_RECURSIVE,
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
    let mut payload = unsafe {
        Command::new("/wormhole-attach")
            .arg(serde_json::to_string(&config)?)
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

    trace!("starting payload");
    {
        let client_writer_m = Arc::clone(&client_writer_m);
        let _: JoinHandle<anyhow::Result<()>> = task::spawn(async move {
            let mut buf = [0u8; 1024];
            loop {
                match payload_stdout.read(&mut buf).await {
                    Ok(0) => break,
                    Ok(n) => {
                        let mut client_writer = client_writer_m.lock().await;
                        // let mut client_writer = client_stdout.lock().await;
                        let _ = RpcOutputMessage::StdioData(1, &buf[..n])
                            .write_to(&mut client_writer)
                            .await
                            .map_err(|e| trace!("error writing to client {e}"));
                    }
                    Err(e) => trace!("got error reading from payload stdout: {e}"),
                }
            }
            Ok(())
        });
    }

    {
        let client_writer_m = Arc::clone(&client_writer_m);
        let _: JoinHandle<anyhow::Result<()>> = task::spawn(async move {
            let mut buf = [0u8; 1024];
            loop {
                match payload_stderr.read(&mut buf).await {
                    Ok(0) => break,
                    Ok(n) => {
                        let mut client_writer = client_writer_m.lock().await;
                        let _ = RpcOutputMessage::StdioData(2, &buf[..n])
                            .write_to(&mut client_writer)
                            .await
                            .map_err(|e| trace!("error writing to client {e}"));
                    }
                    Err(e) => trace!("got error reading from payload stderr: {e}"),
                }
            }
            Ok(())
        });
    }

    let _: JoinHandle<anyhow::Result<()>> = task::spawn(async move {
        loop {
            match RpcInputMessage::read_from(&mut client_stdin).await {
                Ok(RpcInputMessage::StdinData(data)) => {
                    trace!("rpc: stdin data {:?}", String::from_utf8_lossy(&data));
                    let _ = payload_stdin
                        .write(&data)
                        .await
                        .map_err(|e| trace!("error writing to payload {e}"));
                }
                Ok(RpcInputMessage::TerminalResize(w, h)) => {
                    if !is_pty {
                        panic!("cannot resize terminal for non-tty ")
                    }
                    let ws = Winsize {
                        ws_row: h,
                        ws_col: w,
                        ws_xpixel: 0,
                        ws_ypixel: 0,
                    };
                    unsafe {
                        nix::libc::ioctl(payload_stdin.as_raw_fd(), TIOCSWINSZ, &ws);
                    }
                }
                Ok(RpcInputMessage::RequestPty(_pty)) => {
                    trace!("cannot request pty after payload already started");
                }
                Ok(RpcInputMessage::StartPayload(_param)) => {
                    trace!("already started");
                }
                Err(_) => {
                    trace!("rpc: failed to read");
                }
                _ => {}
            }
        }
    });

    task::spawn(async move {
        let mut exit_code = [0u8];
        let _ = exit_code_pipe_reader.read_exact(&mut exit_code).await;

        let mut client_writer = client_writer_m.lock().await;
        let _ = RpcOutputMessage::Exit(exit_code[0])
            .write_to(&mut client_writer)
            .await
            .map_err(|e| trace!("error sending exit code {e}"));

        // todo: kill all async server tasks belonging to the current connection
        trace!("exiting process with exit code {}", exit_code[0]);
    })
    .await?;

    // loop {
    //     match RpcInputMessage::read_from(&mut client_stdin).await {
    //         Ok(RpcInputMessage::StdinData(data)) => {
    //             trace!("rpc: stdin data {:?}", String::from_utf8_lossy(&data));
    //             // let _ =
    //             //     .write(&data)
    //             //     .await
    //             //     .map_err(|e| trace!("error writing to payload {e}"));
    //         }
    //         Ok(RpcInputMessage::TerminalResize(w, h)) => {
    //             // if !is_pty {
    //             //     panic!("cannot resize terminal for non-tty ")
    //             // }
    //             // let ws = Winsize {
    //             //     ws_row: h,
    //             //     ws_col: w,
    //             //     ws_xpixel: 0,
    //             //     ws_ypixel: 0,
    //             // };
    //             // unsafe {
    //             //     nix::libc::ioctl(payload_stdin.as_raw_fd(), TIOCSWINSZ, &ws);
    //             // }
    //         }
    //         Ok(RpcInputMessage::RequestPty(_pty)) => {
    //             trace!("cannot request pty after payload already started");
    //         }
    //         Ok(RpcInputMessage::Start()) => {
    //             trace!("already started");
    //         }
    //         Err(e) => {
    //             trace!("{:?}", e);
    //             // break;
    //             // trace!("rpc: failed to read");
    //         }
    //     }
    // }
    // if connect, then fork and exec into wormhole-attach process with

    // otherwise, rpc traffic should be directed to wormhole-attach

    // spawn async tasks to handle io from client
    Ok(())
}

// todo: keep track of active connections and shutdown server (and delete /rpc.sock) if no active connections
#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    let _ = std::fs::remove_file("/data/rpc.sock");
    let listener = UnixListener::bind("/data/rpc.sock")?;

    trace!("wormhole setup");
    startup()?;

    loop {
        match listener.accept() {
            Ok((mut stream, _)) => {
                tokio::spawn(async move {
                    let _ = handle_client(stream).await;
                });
            }
            Err(e) => {
                trace!("error accepting connection: {:?}", e);
            }
        }
    }

    Ok(())
}
