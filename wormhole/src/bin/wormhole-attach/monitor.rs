use std::{
    fs::File,
    io::{stderr, stdout, Read, Write},
    net::{TcpListener, TcpStream},
    os::{
        fd::{AsFd as _, AsRawFd as _, BorrowedFd, FromRawFd as _, OwnedFd, RawFd},
        unix::net::{UnixListener, UnixStream},
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
        socket::{sendmsg, ControlMessage, MsgFlags},
        stat::{fstatat, Mode},
    },
    unistd::{dup2, getpid, Pid},
};
use tracing::trace;
use wormhole::flock::{Flock, FlockMode, FlockWait};

use crate::{
    model::WormholeConfig,
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
    config: &WormholeConfig,
    proc_fd: OwnedFd,
    nix_flock_ref: Flock,
    forward_signal_fd: OwnedFd,
    send_client_socket_fd: OwnedFd,
    cgroup_path: &str,
    intermediate: Pid,
    sfd: SignalFd,
) -> anyhow::Result<()> {
    // switch over to using the log_fd. if we don't switch, logging will crash the application when stout and stderr closes!
    dup2(config.log_fd, stdout().as_raw_fd())?;
    dup2(config.log_fd, stderr().as_raw_fd())?;
    let forward_signal_socket = UnixStream::from(forward_signal_fd);

    // let rpc_server_socket = "/rpc_server.sock2";
    // trace!("starting rpc server");
    // let listener = UnixListener::bind(rpc_server_socket)?;
    // listener
    //     .set_nonblocking(true)
    //     .expect("Cannot set non-blocking");

    // trace!("waiting for accept");
    // match listener.accept() {
    //     Ok((mut socket, addr)) => {
    //         trace!("new client: {addr:?}");

    //         let mut buf = [0u8; 1024];
    //         let n = socket.read(&mut buf)?;

    //         trace!("read {} bytes", n);
    //     }

    //     Err(e) => trace!("couldn't get client: {e:?}"),
    // }
    // wait until child (intermediate) exits
    trace!("waitpid");
    if let ExitResult::Code(0) = wait_for_exit(intermediate)? {
        if let Err(err) = monitor(
            forward_signal_socket,
            // listener,
            send_client_socket_fd,
            intermediate,
            sfd,
        ) {
            trace!(?err, "monitoring errored");
        }
        trace!("monitoring finished");
    } else {
        trace!("intermediate failed");
    }

    cleanup(proc_fd.as_fd(), nix_flock_ref, cgroup_path)?;

    Ok(())
}

// send the rpc client fd connection to the payload process so that they can communicate directly
// fn send_client_fd(send_client_socket_fd: RawFd, client_fd: RawFd) -> anyhow::Result<()> {
//     let fds = [client_fd];
//     let cmsg = ControlMessage::ScmRights(&fds);
//     let iov = [];
//     sendmsg::<()>(
//         send_client_socket_fd,
//         &iov,
//         &[cmsg],
//         MsgFlags::empty(),
//         None,
//     )?;
//     Ok(())
// }

fn handle_connection(mut stream: UnixStream) {
    let mut buffer = [0; 1024];
    match stream.read(&mut buffer) {
        Ok(n) if n > 0 => {
            println!("Received: {}", String::from_utf8_lossy(&buffer[..n]));
            let _ = stream.write(b"Hello from server!\n");
        }
        _ => {
            eprintln!("Failed to read from connection");
        }
    }
}

fn monitor(
    forward_signal_socket: UnixStream,
    // rpc_listener: UnixListener,
    send_client_socket_fd: OwnedFd,
    intermediate: Pid,
    mut sfd: SignalFd,
) -> anyhow::Result<()> {
    trace!("entering main event loop");
    // let rpc_server_fd = rpc_listener.as_fd();

    let epoll = Epoll::new(EpollCreateFlags::EPOLL_CLOEXEC)?;
    epoll.add(&sfd, EpollEvent::new(EpollFlags::EPOLLIN, 1))?;
    // epoll.add(&rpc_server_fd, EpollEvent::new(EpollFlags::EPOLLIN, 2))?;

    let mut events = [EpollEvent::empty(); 2];

    // intermediate succeeded, we assume the subreaper gets reparented to us and that we will receive SIGCHLD when it exits
    loop {
        let nfds = epoll.wait(&mut events, -1);
        trace!("got epoll events: {:?}", nfds);
        match nfds {
            Ok(n) if n < 1 => {
                return Err(anyhow!("expected an event on epoll return"));
            }
            Err(Errno::EINTR) => continue,
            Err(err) => {
                return Err(anyhow!("error while epolling: {}", err));
            }
            Ok(_) => {}
        }

        for i in 0..nfds? {
            trace!("event type: {}", events[i].data());
            if events[i].data() == 1 {
                match sfd.read_signal() {
                    Ok(Some(sig)) if sig.ssi_signo == Signal::SIGCHLD as u32 => {
                        let mut should_break = false;

                        reap_children(|pid, _| {
                            if pid != intermediate {
                                should_break = true;
                            }
                        })?;
                        if should_break {
                            break;
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
                                    break;
                                }
                            }
                            Err(err) => trace!(?err, "couldn't map signal"),
                        }
                    }
                    result => trace!(?result, "unexpected read_signal result"),
                }
            } else if events[i].data() == 2 {
                // match rpc_listener.accept() {
                //     Ok((stream, addr)) => {
                //         trace!("received connection from {:?}", addr);
                //         stream.set_nonblocking(true)?;
                //         // send to child process via unix socket and SCM_RIGHTS
                //         trace!(
                //             "sending client_fd {:?} to the payload process",
                //             stream.as_raw_fd()
                //         );
                //         // send_client_fd(send_client_socket_fd.as_raw_fd(), stream.as_raw_fd())?;

                //         handle_connection(stream);
                //     }
                //     Err(e) => trace!("could not accept connection {:?}", e),
                // }
            }
        }
    }

    trace!("subreaper exited");
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
