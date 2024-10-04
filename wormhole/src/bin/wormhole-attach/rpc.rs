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
    unistd::{close, dup, dup2, execve, fork, pipe, setsid, ForkResult},
};
use std::{
    collections::HashMap,
    ffi::CString,
    fs::File,
    io::{self, Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd, RawFd},
        unix::net::UnixStream,
    },
    process,
};
use tracing::trace;

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

    pub fn write_to(&self, stream: &mut impl Write) -> anyhow::Result<()> {
        stream.write_all(&[self.to_const()])?;

        match self {
            Self::StdioData(fd, data) => {
                trace!("writing {} bytes", data.len() + 1);
                let len_bytes = u32::try_from(data.len() + 1)?.to_be_bytes();
                trace!("len bytes {:?} bytes", len_bytes);
                stream.write_all(&len_bytes)?;
                stream.write(&[*fd])?;
                stream.write_all(data)?;
            }
            Self::Exit(exit_code) => stream.write_all(&[*exit_code])?,
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

fn read_bytes(stream: &mut impl Read) -> anyhow::Result<Vec<u8>> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        stream.read_exact(&mut len_bytes)?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut data = vec![0_u8; len];
    stream.read_exact(&mut data)?;
    Ok(data)
}

fn read_u16(stream: &mut impl Read) -> anyhow::Result<u16> {
    let mut buf = [0_u8; size_of::<u16>()];
    stream.read_exact(&mut buf)?;
    Ok(u16::from_be_bytes(buf))
}

impl RpcInputMessage {
    pub fn read_from(stream: &mut impl Read) -> anyhow::Result<Self> {
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte)?;
            RpcType::from_const(rpc_type_byte[0])
        };
        match rpc_type {
            RpcType::ReadStdin => {
                let data = read_bytes(stream)?;
                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::WindowChange => {
                let h = read_u16(stream)?;
                let w = read_u16(stream)?;
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::RequestPty => {
                let term_env = String::from_utf8(read_bytes(stream)?)?;
                let h = read_u16(stream)?;
                let w = read_u16(stream)?;
                let termios_config = read_bytes(stream)?;
                let pty = create_pty(w, h, termios_config)?;

                Ok(RpcInputMessage::RequestPty(PtyConfig { pty, term_env }))
            }
            RpcType::Start => Ok(RpcInputMessage::Start()),
        }
    }
}

fn set_nonblocking(fd: RawFd) -> nix::Result<()> {
    let flags = fcntl(fd, FcntlArg::F_GETFL)?;
    let new_flags = OFlag::from_bits_truncate(flags) | OFlag::O_NONBLOCK;
    fcntl(fd, FcntlArg::F_SETFL(new_flags))?;
    Ok(())
}

pub fn run(
    config: WormholeConfig,
    client_fd: RawFd,
    mut exit_code_reader: UnixStream,
    env_map: &mut HashMap<String, String>,
) -> anyhow::Result<()> {
    // dup2(config.log_fd, stdout().as_raw_fd())?;
    // dup2(config.log_fd, stderr().as_raw_fd())?;

    let shell_cmd = config.entry_shell_cmd.unwrap_or_else(|| "".to_string());
    let mut client = unsafe { File::from_raw_fd(client_fd) };
    let mut pty: Option<OpenptyResult> = None;

    let mut stdin_pipe = (-1, -1);
    let mut stdout_pipe = (-1, -1);
    let mut stderr_pipe = (-1, -1);

    // wait until user calls start before proceeding
    loop {
        match RpcInputMessage::read_from(&mut client) {
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

                env_map.insert("TERM".to_string(), pty_config.term_env);
                trace!("env map")
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

    trace!("finished reading host");

    match unsafe { fork()? } {
        // child: payload
        ForkResult::Parent { child: _ } => {
            close(stdin_pipe.1)?;
            close(stdout_pipe.0)?;
            close(stderr_pipe.0)?;

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

            let cstr_envs = env_map
                .iter()
                .map(|(k, v)| CString::new(format!("{}={}", k, v)))
                .collect::<anyhow::Result<Vec<_>, _>>()?;

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
            close(stdin_pipe.0)?;
            close(stdout_pipe.1)?;
            close(stderr_pipe.1)?;

            // write to payload stdin and read from stdout/stderr
            let mut payload_stdin = unsafe { File::from_raw_fd(stdin_pipe.1) };
            let mut payload_stdout = unsafe { File::from_raw_fd(stdout_pipe.0) };
            let mut payload_stderr = unsafe { File::from_raw_fd(stderr_pipe.0) };

            let mut client_reader = unsafe { File::from_raw_fd(client_fd) };
            let mut client_writer = client_reader.try_clone()?;
            let mut client_writer2 = client_writer.try_clone()?;

            set_nonblocking(payload_stdout.as_raw_fd())?;
            set_nonblocking(payload_stderr.as_raw_fd())?;
            set_nonblocking(client_reader.as_raw_fd())?;
            set_nonblocking(exit_code_reader.as_raw_fd())?;

            let epoll = Epoll::new(EpollCreateFlags::EPOLL_CLOEXEC)?;
            epoll.add(&payload_stdout, EpollEvent::new(EpollFlags::EPOLLIN, 1))?;
            epoll.add(&payload_stderr, EpollEvent::new(EpollFlags::EPOLLIN, 2))?;
            epoll.add(&client_reader, EpollEvent::new(EpollFlags::EPOLLIN, 3))?;
            epoll.add(&exit_code_reader, EpollEvent::new(EpollFlags::EPOLLIN, 4))?;

            let mut events = [EpollEvent::empty(); 10];
            let mut buffer = [0u8; 1024];
            loop {
                let n = epoll.wait(&mut events, -1);
                match n {
                    Ok(n) if n < 1 => {
                        return Err(anyhow!("expected an event on epoll return"));
                    }
                    Err(Errno::EINTR) => continue,
                    Err(err) => {
                        return Err(anyhow!("error while epolling: {}", err));
                    }
                    Ok(_) => {}
                }
                let n = n?;
                trace!("got {n} events");
                for event in &events[..n] {
                    let data = event.data();

                    // write payload stdout and stderr to client
                    match data {
                        1 | 2 => {
                            let source_fd = if data == 1 {
                                &mut payload_stdout
                            } else {
                                &mut payload_stderr
                            };
                            // Handle reading from payload_stdout
                            match source_fd.read(&mut buffer) {
                                Ok(0) => {
                                    trace!("could not read from payload stdout/stderr ({data})");
                                    epoll.delete(source_fd)?;
                                }
                                Ok(n) => {
                                    trace!(
                                        "rpc: response data {:?}",
                                        String::from_utf8_lossy(&buffer[..n])
                                    );
                                    RpcOutputMessage::StdioData(data as u8, &buffer[..n])
                                        .write_to(&mut client_writer)?;
                                    client_writer.flush()?;
                                }
                                Err(ref e) if e.kind() == io::ErrorKind::WouldBlock => {
                                    trace!("would block");
                                    // No data available right now
                                    continue;
                                }
                                Err(e) => {
                                    trace!("reading from stdout/stderr ({data}): {:?}", e);
                                    epoll.delete(source_fd)?;
                                }
                            }
                        }
                        3 => {
                            trace!("reading from client");
                            match RpcInputMessage::read_from(&mut client_reader) {
                                Ok(RpcInputMessage::StdinData(data)) => {
                                    trace!("rpc: stdin data {:?}", String::from_utf8_lossy(&data));
                                    payload_stdin.write_all(&data)?;
                                    payload_stdin.flush()?
                                }
                                Ok(RpcInputMessage::TerminalResize(w, h)) => {
                                    if pty.is_none() {
                                        panic!("cannot resize terminal for non-tty ")
                                    }
                                    let ws = Winsize {
                                        ws_row: h,
                                        ws_col: w,
                                        ws_xpixel: 0, // Not used, can be set to 0
                                        ws_ypixel: 0, // Not used, can be set to 0
                                    };
                                    unsafe {
                                        nix::libc::ioctl(
                                            payload_stdin.as_raw_fd(),
                                            TIOCSWINSZ,
                                            &ws,
                                        );
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
                            };
                        }
                        4 => {
                            let mut exit_code = [0u8];
                            exit_code_reader.read_exact(&mut exit_code)?;
                            trace!("read exit code {}", exit_code[0]);

                            RpcOutputMessage::Exit(exit_code[0]).write_to(&mut client_writer2)?;

                            trace!("exiting process");
                            process::exit(exit_code[0].into());
                        }
                        _ => {
                            trace!("unknown epoll data {data}")
                        }
                    }
                }
            }
        }
    }
}
