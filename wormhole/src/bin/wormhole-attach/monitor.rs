use std::{
    fs::File,
    io::{stderr, stdout},
    os::{
        fd::{AsFd as _, AsRawFd as _, BorrowedFd, FromRawFd as _, OwnedFd},
        unix::net::UnixStream,
    },
    path::Path,
};

use anyhow::anyhow;
use nix::{
    errno::Errno,
    fcntl::{openat, AtFlags, OFlag},
    mount::{umount2, MntFlags},
    sys::{
        epoll::{Epoll, EpollCreateFlags, EpollEvent, EpollFlags},
        signal::{kill, Signal},
        stat::{fstatat, Mode},
    },
    unistd::{dup2, getpid, Pid},
};
use tracing::trace;
use wormhole::{
    flock::{Flock, FlockMode, FlockWait},
    model::WormholeRuntimeState,
};

use crate::{
    mounts::with_remount_rw,
    parse_proc_mounts,
    proc::{
        get_ns_of_pid_from_dirfd, iter_pids_from_dirfd, reap_children, wait_for_exit, ExitResult,
    },
    signals::SignalFd,
    subreaper_protocol::Message,
    DIR_CREATE_LOCK,
};

enum DeleteNixDirResult {
    Success,
    Busy,
    NotOurNix,
    ActiveRefs,
}

fn delete_nix_dir(
    proc_fd: BorrowedFd<'_>,
    nix_flock_ref: Flock,
) -> anyhow::Result<DeleteNixDirResult> {
    // try to unmount everything on our view of /nix recursively
    let mounts_file = unsafe {
        File::from_raw_fd(openat(
            proc_fd.as_raw_fd(),
            "thread-self/mounts",
            OFlag::O_RDONLY | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };
    let proc_mounts = parse_proc_mounts(&std::io::read_to_string(mounts_file)?)?;
    for mnt in proc_mounts.iter().rev() {
        if mnt.dest == "/nix" || mnt.dest.starts_with("/nix/") {
            trace!("delete_nix_dir: unmount {}", mnt.dest);
            match umount2(Path::new(&mnt.dest), MntFlags::UMOUNT_NOFOLLOW) {
                Ok(_) => {}
                Err(Errno::EBUSY) => {
                    // still in use (bg / forked process)
                    trace!("delete_nix_dir: mounts still in use");
                    return Ok(DeleteNixDirResult::Busy);
                }
                Err(e) => return Err(e.into()),
            }
        }
    }

    trace!("delete_nix_dir: wait for lock");
    let _flock = Flock::new_ofd(
        File::create(DIR_CREATE_LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;

    // drop our ref
    drop(nix_flock_ref);

    // check whether we created /nix
    if xattr::get("/nix", "user.orbstack.wormhole")?.is_none() {
        // we didn't create /nix, so don't delete it
        trace!("delete_nix_dir: /nix not created by us");
        return Ok(DeleteNixDirResult::NotOurNix);
    }

    // check whether there are any remaining refs
    if Flock::check_ofd(File::open("/nix")?, FlockMode::Exclusive)? {
        // success - no refs; continue
        trace!("delete_nix_dir: no refs");
    } else {
        // there are still active refs, so we can't delete /nix
        trace!("delete_nix_dir: refs still active");
        return Ok(DeleteNixDirResult::ActiveRefs);
    }

    // good to go for deletion:
    // - we created it (according to xattr)
    // - no remaining refs (according to flock)

    trace!("delete_nix_dir: deleting /nix");
    with_remount_rw(|| match std::fs::remove_dir("/nix") {
        Ok(_) => Ok(()),
        // raced with another process
        Err(ref e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e),
    })?;

    Ok(DeleteNixDirResult::Success)
}

fn map_signal(signal: u32) -> anyhow::Result<Signal> {
    if signal == Signal::SIGPWR as u32 {
        return Ok(Signal::SIGKILL);
    }

    if let Ok(fwd_signal) = (signal as i32).try_into() {
        return Ok(fwd_signal);
    }

    Err(anyhow!("unknown signal: {}", signal))
}

pub fn run(
    state: &WormholeRuntimeState,
    proc_fd: OwnedFd,
    nix_flock_ref: Flock,
    forward_signal_fd: OwnedFd,
    cgroup_path: &str,
    intermediate: Pid,
    sfd: SignalFd,
    server_pidfd: Option<OwnedFd>,
) -> anyhow::Result<()> {
    // switch over to using the log_fd. if we don't switch, logging will crash the application when stout and stderr closes!
    dup2(state.log_fd, stdout().as_raw_fd())?;
    dup2(state.log_fd, stderr().as_raw_fd())?;
    let forward_signal_socket = UnixStream::from(forward_signal_fd);

    // wait until child (intermediate) exits
    trace!("waitpid");
    if let ExitResult::Code(0) = wait_for_exit(intermediate)? {
        if let Err(err) = monitor(forward_signal_socket, intermediate, sfd, server_pidfd) {
            trace!(?err, "monitoring errored");
        }
        trace!("monitoring finished");
    } else {
        trace!("intermediate failed");
    }

    cleanup(proc_fd.as_fd(), nix_flock_ref, cgroup_path)?;

    Ok(())
}

fn monitor(
    forward_signal_socket: UnixStream,
    intermediate: Pid,
    mut sfd: SignalFd,
    server_pidfd: Option<OwnedFd>,
) -> anyhow::Result<()> {
    trace!("entering main event loop");

    let epoll = Epoll::new(EpollCreateFlags::EPOLL_CLOEXEC)?;
    epoll.add(&sfd, EpollEvent::new(EpollFlags::EPOLLIN, 1))?;
    if let Some(server_pidfd) = server_pidfd {
        epoll.add(&server_pidfd, EpollEvent::new(EpollFlags::EPOLLIN, 2))?;
    }

    let mut events = [EpollEvent::empty(); 2];

    // intermediate succeeded, we assume the subreaper gets reparented to us and that we will receive SIGCHLD when it exits
    'outer: loop {
        let n = match epoll.wait(&mut events, -1) {
            Ok(n) if n < 1 => {
                return Err(anyhow!("expected an event on epoll return"));
            }
            Ok(n) => n,
            Err(Errno::EINTR) => continue,
            Err(err) => {
                return Err(anyhow!("error while epolling: {}", err));
            }
        };

        for event in &events[0..n] {
            if event.data() == 1 {
                match sfd.read_signal() {
                    Ok(Some(sig)) if sig.ssi_signo == Signal::SIGCHLD as u32 => {
                        let mut should_break = false;

                        reap_children(|pid, _| {
                            if pid != intermediate {
                                should_break = true;
                            }
                        })?;
                        if should_break {
                            break 'outer;
                        }
                    }
                    Ok(Some(sig)) => {
                        trace!(?sig, "got signal");

                        match map_signal(sig.ssi_signo) {
                            Ok(sig_forward) => {
                                if let Err(err) = Message::ForwardSignal(sig_forward as i32)
                                    .write_to(&forward_signal_socket)
                                {
                                    trace!(?err, "couldn't forward signal via socket");
                                    break 'outer;
                                }
                            }
                            Err(err) => trace!(?err, "couldn't map signal"),
                        }
                    }
                    result => trace!(?result, "unexpected read_signal result"),
                }
            } else if event.data() == 2 {
                // if the server is abruptly killed, break out of loop and start background task cleanup
                break 'outer;
            } else {
                trace!("unexpected epoll event {}", event.data());
            }
        }
    }

    trace!("monitor exited");
    Ok(())
}

fn cleanup(
    proc_fd: BorrowedFd<'_>,
    nix_flock_ref: Flock,
    _cgroup_path: &str,
) -> anyhow::Result<()> {
    trace!("cleaning up");

    // save the mountns so we can check if pids are in it
    let wormhole_mountns = fstatat(proc_fd.as_raw_fd(), "./self/ns/mnt", AtFlags::empty())?.st_ino;

    let self_pid = getpid();

    loop {
        let mut found_pids = 0;
        for pid in iter_pids_from_dirfd(proc_fd.as_fd())? {
            let pid = pid.map_err(|err| anyhow!("error while iterating through pids: {}", err))?;

            // if we kill ourselves, we exit before we're done doing things -- that's bad!
            if pid == self_pid {
                continue;
            }

            if let Ok(mountns) = get_ns_of_pid_from_dirfd(proc_fd.as_fd(), pid, "mnt") {
                if mountns == wormhole_mountns {
                    found_pids += 1;

                    trace!(%pid, "stopping process");
                    if let Err(err) = kill(pid, Some(Signal::SIGKILL)) {
                        trace!(%pid, ?err, "error while kill process");
                    }
                }
            }
        }
        if found_pids == 0 {
            break;
        }
    }

    reap_children(|_, _| {})?;

    // try to delete /nix
    if let DeleteNixDirResult::Busy = delete_nix_dir(proc_fd.as_fd(), nix_flock_ref)? {
        trace!("mounts are still busy, can't unmount")
    }

    Ok(())
}
