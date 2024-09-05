use std::{
    fs::File,
    io::{stderr, stdout, Write},
    os::{
        fd::{AsRawFd, FromRawFd as _, OwnedFd},
        unix::net::UnixStream,
    },
};

use anyhow::{bail, Result};
use nix::{
    errno::Errno,
    sys::{
        epoll::{Epoll, EpollCreateFlags, EpollEvent, EpollFlags},
        signal::kill,
        signalfd::SignalFd,
        wait::{waitid, Id, WaitPidFlag, WaitStatus},
    },
    unistd::{dup2, Pid},
};
use tracing::trace;

use crate::{model::WormholeConfig, protocol::Message};

fn return_exit_code(mut stream: impl Write, exit_code: i32) -> anyhow::Result<()> {
    stream.write_all(&[exit_code as u8])?; // should be fine since exit codes can only be 0-255
    stream.flush()?;
    Ok(())
}

/// Returns a boolean indicating whether child processes still exist
fn reap_children(mut process_exited_cb: impl FnMut(Pid, i32)) -> Result<bool> {
    loop {
        match waitid(Id::All, WaitPidFlag::WNOHANG | WaitPidFlag::WEXITED) {
            Ok(WaitStatus::Exited(pid, status)) => process_exited_cb(pid, status),
            Ok(WaitStatus::Signaled(pid, signal, _)) => process_exited_cb(pid, signal as i32 + 128),
            Ok(WaitStatus::StillAlive) => return Ok(true),
            Ok(_) => {}
            Err(Errno::ECHILD) => return Ok(false),
            Err(err) => return Err(err.into()),
        }
    }
}

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
    epoll.add(&sfd, EpollEvent::new(EpollFlags::EPOLLIN, 1))?;
    epoll.add(&socket, EpollEvent::new(EpollFlags::EPOLLIN, 2))?;

    let mut events = [EpollEvent::empty()];

    let mut payload_pid = Some(payload_pid);

    loop {
        if epoll.wait(&mut events, -1)? < 1 {
            bail!("expected an event on epoll return.");
        }

        match events[0].data() {
            // sfd
            1 => match sfd.read_signal() {
                Ok(Some(_sig)) => {
                    trace!("caught a signal, reaping.");

                    let mut process_exited_cb = |pid, status| {
                        if !payload_pid.is_some_and(|payload_pid| payload_pid == pid) {
                            return;
                        }

                        payload_pid = None;

                        if let Err(err) = return_exit_code(&mut exit_code_pipe_write, status) {
                            trace!(?err, "error returning exit code.");
                        }
                    };

                    if !reap_children(&mut process_exited_cb)? {
                        trace!("no more children, exiting.");
                        break;
                    }
                }
                Ok(None) => {}
                Err(err) => trace!(?err, "error while trying to read signal from sfd."),
            },
            // socket
            2 => match Message::read_from(&socket)? {
                Message::ForwardSignal(sig) => {
                    if let Some(payload_pid) = payload_pid {
                        kill(payload_pid, Some(sig.try_into()?))?;
                    }
                }
            },
            _ => bail!("unexpected epoll data."),
        }
    }

    // if we close socket, monitor won't finish receiving our last message
    Ok(())
}
