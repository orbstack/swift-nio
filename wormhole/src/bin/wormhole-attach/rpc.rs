use anyhow::anyhow;
use libc::{epoll_wait, EWOULDBLOCK, TIOCSCTTY, TIOCSWINSZ};
use nix::{
    errno::Errno,
    fcntl::{fcntl, FcntlArg, OFlag},
    libc::ioctl,
    pty::{openpty, OpenptyResult, Winsize},
    sys::{
        epoll::{Epoll, EpollCreateFlags, EpollEvent, EpollFlags},
        termios::{tcgetattr, tcsetattr, SetArg, Termios},
    },
    unistd::{close, dup, dup2, execve, fork, pipe, setsid, sleep, ForkResult},
};
use std::{
    collections::HashMap,
    ffi::CString,
    fs::File,
    io::{self, Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd, RawFd},
        unix::net::UnixStream,
    },
    path::Path,
    process,
};
use tokio::{
    io::{unix::AsyncFd, AsyncReadExt, AsyncWrite, AsyncWriteExt},
    task::{self, JoinHandle},
};
use tracing::trace;
use wormhole::asyncfile::AsyncFile;

use crate::{
    model::WormholeConfig,
    termios::{create_pty, set_termios},
};

#[derive(Debug, PartialEq, Eq)]
pub enum RpcType {
    ReadStdin = 1,
    WindowChange = 2,
    RequestPty = 3,
    Start = 4,
}

impl RpcType {
    pub fn from_const(rpc_type: u8) -> Self {
        match rpc_type {
            1 => Self::ReadStdin,
            2 => Self::WindowChange,
            3 => Self::RequestPty,
            4 => Self::Start,
            _ => panic!("invalid rpc type {rpc_type}"),
        }
    }
}

pub enum RpcOutputMessage<'a> {
    StdioData(u8, &'a [u8]),
    Exit(u8),
}

impl<'a> RpcOutputMessage<'a> {
    pub fn to_const(&self) -> u8 {
        match self {
            Self::StdioData(_, _) => 1,
            Self::Exit(_) => 2,
        }
    }

    pub async fn write_to(&self, stream: &mut AsyncFile) -> anyhow::Result<()> {
        stream.write(&[self.to_const()]).await?;

        match self {
            Self::StdioData(fd, data) => {
                trace!("writing {} bytes", data.len() + 1);
                let len_bytes = u32::try_from(data.len() + 1)?.to_be_bytes();
                trace!("len bytes {:?} bytes", len_bytes);
                stream.write(&len_bytes).await?;
                stream.write(&[*fd]).await?;
                stream.write(data).await?
            }
            Self::Exit(exit_code) => stream.write(&[*exit_code]).await?,
        };

        Ok(())
    }
}

#[derive(Debug)]
pub struct PtyConfig {
    pub pty: OpenptyResult,
    pub term_env: String,
}

pub enum RpcInputMessage {
    StdinData(Vec<u8>),
    TerminalResize(u16, u16),
    RequestPty(PtyConfig),
    Start(),
}

impl RpcInputMessage {}

fn read_bytes_sync(stream: &mut impl Read) -> anyhow::Result<Vec<u8>> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        stream.read_exact(&mut len_bytes)?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut data = vec![0_u8; len];
    stream.read_exact(&mut data)?;
    Ok(data)
}

fn read_u16_sync(stream: &mut impl Read) -> anyhow::Result<u16> {
    let mut buf = [0_u8; size_of::<u16>()];
    stream.read_exact(&mut buf)?;
    Ok(u16::from_be_bytes(buf))
}

async fn read_bytes(stream: &mut AsyncFile) -> anyhow::Result<Vec<u8>> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        stream.read_exact(&mut len_bytes).await?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut data = vec![0_u8; len];
    stream.read_exact(&mut data).await?;
    Ok(data)
}

async fn read_u16(stream: &mut AsyncFile) -> anyhow::Result<u16> {
    let mut buf = [0_u8; size_of::<u16>()];
    stream.read_exact(&mut buf).await?;
    Ok(u16::from_be_bytes(buf))
}

impl RpcInputMessage {
    pub fn read_from_sync(stream: &mut impl Read) -> anyhow::Result<Self> {
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte)?;
            RpcType::from_const(rpc_type_byte[0])
        };
        match rpc_type {
            RpcType::ReadStdin => {
                let data = read_bytes_sync(stream)?;
                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::WindowChange => {
                let h = read_u16_sync(stream)?;
                let w = read_u16_sync(stream)?;
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::RequestPty => {
                let term_env = String::from_utf8(read_bytes_sync(stream)?)?;
                let h = read_u16_sync(stream)?;
                let w = read_u16_sync(stream)?;
                let termios_config = read_bytes_sync(stream)?;
                let pty = create_pty(w, h, termios_config)?;

                Ok(RpcInputMessage::RequestPty(PtyConfig { pty, term_env }))
            }
            RpcType::Start => Ok(RpcInputMessage::Start()),
        }
    }

    pub async fn read_from(stream: &mut AsyncFile) -> anyhow::Result<Self> {
        trace!("reading from client");
        // Ok(())
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte).await?;
            RpcType::from_const(rpc_type_byte[0])
        };
        trace!("got rpc type {:?}", rpc_type);
        match rpc_type {
            RpcType::ReadStdin => {
                let data = read_bytes(stream).await?;
                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::WindowChange => {
                let h = read_u16(stream).await?;
                let w = read_u16(stream).await?;
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::RequestPty => {
                let term_env = String::from_utf8(read_bytes(stream).await?)?;
                let h = read_u16(stream).await?;
                let w = read_u16(stream).await?;
                let termios_config = read_bytes(stream).await?;
                let pty = create_pty(w, h, termios_config)?;

                Ok(RpcInputMessage::RequestPty(PtyConfig { pty, term_env }))
            }
            RpcType::Start => Ok(RpcInputMessage::Start()),
        }
    }
}

async fn run_rpc_server(
    payload_stdin: RawFd,
    payload_stdout: RawFd,
    payload_stderr: RawFd,
    client: (File, File),
    mut exit_code_reader: UnixStream,
    is_pty: bool,
) -> anyhow::Result<()> {
    // note: we must create the AsyncFile within a Tokio context, since it relies on AsyncFd
    let mut payload_stdin = AsyncFile::new(payload_stdin)?;
    let mut payload_stdout = AsyncFile::new(payload_stdout)?;
    let mut payload_stderr = AsyncFile::new(payload_stderr)?;

    let mut client_reader = AsyncFile::from(client.0)?;
    let mut client_writer_stdout = AsyncFile::from(client.1)?;
    let mut client_writer_stderr = client_writer_stdout.try_clone()?;
    let mut client_writer_exit = client_writer_stdout.try_clone()?;

    let _: JoinHandle<anyhow::Result<()>> = task::spawn(async move {
        let mut buf = [0u8; 1024];
        loop {
            match payload_stdout.read(&mut buf).await {
                Ok(0) => break,
                Ok(n) => {
                    let _ = RpcOutputMessage::StdioData(1, &buf[..n])
                        .write_to(&mut client_writer_stdout)
                        .await
                        .map_err(|e| trace!("error writing to client {e}"));
                }
                Err(e) => trace!("got error reading from payload stdout: {e}"),
            }
        }
        Ok(())
    });
    let _: JoinHandle<anyhow::Result<()>> = task::spawn(async move {
        let mut buf = [0u8; 1024];
        loop {
            match payload_stderr.read(&mut buf).await {
                Ok(0) => break,
                Ok(n) => {
                    let _ = RpcOutputMessage::StdioData(2, &buf[..n])
                        .write_to(&mut client_writer_stderr)
                        .await
                        .map_err(|e| trace!("error writing to client {e}"));
                }
                Err(e) => trace!("got error reading from payload stdout: {e}"),
            }
        }
        Ok(())
    });

    let _: JoinHandle<anyhow::Result<()>> = task::spawn(async move {
        loop {
            match RpcInputMessage::read_from(&mut client_reader).await {
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
                Ok(RpcInputMessage::Start()) => {
                    trace!("already started");
                }
                Err(_) => {
                    trace!("rpc: failed to read");
                }
            }
        }
    });

    let mut exit_code_reader = AsyncFile::new(dup(exit_code_reader.as_raw_fd())?)?;
    task::spawn(async move {
        let mut exit_code = [0u8];
        let _ = exit_code_reader.read_exact(&mut exit_code).await;
        trace!("read exit code {}", exit_code[0]);

        let _ = RpcOutputMessage::Exit(exit_code[0])
            .write_to(&mut client_writer_exit)
            .await
            .map_err(|e| trace!("error sending exit code {e}"));

        trace!("exiting process");
        process::exit(exit_code[0].into());
    })
    .await?;
    Ok(())
}

pub fn run(
    config: WormholeConfig,
    mut client: (File, File),
    mut exit_code_reader: UnixStream,
    shell_cmd: &str,
    mut cstr_envs: Vec<CString>, // env_map: &mut HashMap<String, String>,
) -> anyhow::Result<()> {
    // dup2(config.log_fd, stdout().as_raw_fd())?;
    // dup2(config.log_fd, stderr().as_raw_fd())?;

    trace!("rpc server");

    let mut pty: Option<OpenptyResult> = None;

    let mut stdin_pipe: (RawFd, RawFd) = (-1, -1);
    let mut stdout_pipe: (RawFd, RawFd) = (-1, -1);
    let mut stderr_pipe: (RawFd, RawFd) = (-1, -1);

    // let mut client_reader = AsyncFile::from(client_stdin.try_clone()?)?;
    // let mut client_writer = AsyncFile::from(client_stdout.try_clone()?)?;

    // wait until user calls start before proceeding
    loop {
        match RpcInputMessage::read_from_sync(&mut client.0) {
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

                cstr_envs.push(CString::new(format!("TERM={}", pty_config.term_env))?);
            }
            Ok(RpcInputMessage::Start()) => break,
            _ => {}
        };
    }

    if pty.is_none() {
        stdin_pipe = pipe()?;
        stdout_pipe = pipe()?;
        stderr_pipe = pipe()?;
    }

    // let stdin_pipe = (unsafe { OwnedFd::from_raw_fd(stdin_pipe.0) }, unsafe {
    //     OwnedFd::from_raw_fd(stdin_pipe.1)
    // });
    // let stdout_pipe = (unsafe { OwnedFd::from_raw_fd(stdout_pipe.0) }, unsafe {
    //     OwnedFd::from_raw_fd(stdout_pipe.1)
    // });
    // let stderr_pipe = (unsafe { OwnedFd::from_raw_fd(stderr_pipe.0) }, unsafe {
    //     OwnedFd::from_raw_fd(stderr_pipe.1)
    // });

    trace!("finished reading host");

    match unsafe { fork()? } {
        // child: payload
        ForkResult::Parent { child: _ } => {
            // close(stdin_pipe.1)?;
            // close(stdout_pipe.0)?;
            // close(stderr_pipe.0)?;

            if pty.is_some() {
                setsid()?;
                unsafe {
                    ioctl(stdin_pipe.0, TIOCSCTTY, 1);
                }
            }
            // read from stdin and write to stdout/stderr
            dup2(stdin_pipe.0, libc::STDIN_FILENO)?;
            dup2(stdout_pipe.1, libc::STDOUT_FILENO)?;
            dup2(stderr_pipe.1, libc::STDERR_FILENO)?;

            execve(
                &CString::new("/nix/orb/sys/bin/dctl")?,
                &[
                    CString::new("dctl")?,
                    CString::new("__entrypoint")?,
                    CString::new("--")?,
                    CString::new(shell_cmd)?,
                ],
                &cstr_envs,
            )?;
            unreachable!();
        }
        ForkResult::Child => {
            // we should only start the tokio runtime after the fork, otherwise may cause undefined tokio behaviour
            let rt = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()?;
            rt.block_on(run_rpc_server(
                stdin_pipe.1,
                stdout_pipe.0,
                stderr_pipe.0,
                client,
                exit_code_reader,
                pty.is_some(),
            ))
        }
    }
}
