use std::{
    fs::File,
    io::{stderr, stdout, Write},
    os::{
        fd::{AsRawFd, FromRawFd as _, OwnedFd},
        unix::net::UnixStream,
    },
};

use anyhow::{anyhow};
use nix::{
    errno::Errno,
    sys::{
        epoll::{Epoll, EpollCreateFlags, EpollEvent, EpollFlags},
        signal::{self, kill},
    },
    unistd::{dup2, Pid},
};
use tracing::trace;

use crate::{
    model::WormholeConfig, proc::reap_children, signals::SignalFd, subreaper_protocol::Message,
};

fn return_exit_code(mut stream: impl Write, exit_code: i32) -> anyhow::Result<()> {
    stream.write_all(&[exit_code as u8])?; // should be fine since exit codes can only be 0-255
    stream.flush()?;
    Ok(())
}

const EPOLL_SFD_DATA: u64 = 1;
const EPOLL_SOCKET_DATA: u64 = 2;

pub fn run(
    config: &WormholeConfig,
    socket_fd: OwnedFd,
    mut sfd: SignalFd,
    payload_pid: Pid,
) -> anyhow::Result<()> {
    // switch over to using the log_fd. if we don't switch, logging will crash the application when stout and stderr closes!
    dup2(config.log_fd, stdout().as_raw_fd())?;
    dup2(config.log_fd, stderr().as_raw_fd())?;

    let mut exit_code_pipe_write = unsafe { File::from_raw_fd(config.exit_code_pipe_write_fd) };
    let socket = UnixStream::from(socket_fd);

    let epoll = Epoll::new(EpollCreateFlags::EPOLL_CLOEXEC)?;
    epoll.add(&sfd, EpollEvent::new(EpollFlags::EPOLLIN, EPOLL_SFD_DATA))?;
    epoll.add(
        &socket,
        EpollEvent::new(EpollFlags::EPOLLIN, EPOLL_SOCKET_DATA),
    )?;

    let mut events = [EpollEvent::empty()];

    let mut payload_pid = Some(payload_pid);

    loop {
        match epoll.wait(&mut events, -1) {
            Ok(n) if n < 1 => {
                return Err(anyhow!("expected an event on epoll return"));
            }
            Err(Errno::EINTR) => continue,
            Err(err) => {
                return Err(anyhow!("error while epolling: {}", err));
            }
            Ok(_) => {}
        }

        match events[0].data() {
            EPOLL_SFD_DATA => match sfd.read_signal() {
                Ok(Some(sig)) if sig.ssi_signo == signal::SIGCHLD as u32 => {
                    trace!("caught a signal, reaping");

                    let mut process_exited_cb = |pid, status| {
                        if !payload_pid.is_some_and(|payload_pid| payload_pid == pid) {
                            return;
                        }

                        payload_pid = None;

                        if let Err(err) = return_exit_code(&mut exit_code_pipe_write, status) {
                            trace!(?err, "error returning exit code");
                        }
                    };

                    if !reap_children(&mut process_exited_cb)? {
                        trace!("no more children, exiting");
                        break;
                    }
                }
                Ok(_) => {}
                Err(err) => trace!(?err, "error while trying to read signal from sfd"),
            },
            EPOLL_SOCKET_DATA => match Message::read_from(&socket)? {
                Message::ForwardSignal(sig) => {
                    trace!(sig, "forwarding signal");
                    if let Some(payload_pid) = payload_pid {
                        kill(payload_pid, Some(sig.try_into()?))?;
                    }
                }
            },
            _ => return Err(anyhow!("unexpected epoll data")),
        }
    }

    // if we close socket, monitor won't finish receiving our last message
    Ok(())
}
