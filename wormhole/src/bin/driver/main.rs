// driver for wormhole-attach that opens the mount and gets container info

// ./driver <pid> <container env> ... --> runs wormhole-attach with the proper mount / fds

use libc::{DIR, FD_CLOEXEC};
use nix::{
    fcntl::{
        fcntl,
        FcntlArg::{self, F_GETFD},
        FdFlag,
    },
    mount::{self, mount, umount2, MntFlags, MsFlags},
    sys::wait::waitpid,
    unistd::{execve, execvp, fork, pipe, read, ForkResult, ROOT},
};
use serde::{Deserialize, Serialize};
use std::{
    env,
    ffi::CString,
    fs::{self, read_to_string, File, OpenOptions},
    io, mem,
    os::fd::{AsRawFd, FromRawFd},
    path::Path,
    process::Command,
    thread,
};
use tracing::{debug, span, trace, Level};
use tracing_subscriber::fmt::format::{self, FmtSpan};
use wormhole::{
    flock::{Flock, FlockMode, FlockWait},
    newmount::open_tree,
};

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    pub init_pid: i32,
    #[serde(default)]
    pub wormhole_mount_tree_fd: i32,
    #[serde(default)]
    pub exit_code_pipe_write_fd: i32,
    #[serde(default)]
    pub log_fd: i32,
    #[serde(default)]
    pub drm_token: String,

    pub container_env: Option<Vec<String>>,
    pub container_workdir: Option<String>,

    pub entry_shell_cmd: Option<String>,
}

const ROOTFS: &str = "/wormhole-rootfs";
const UPPERDIR: &str = "/data/upper";
const WORKDIR: &str = "/data/work";
const WORMHOLE_OVERLAY: &str = "/mnt/wormhole-overlay";
const WORMHOLE_UNIFIED: &str = "/mnt/wormhole-unified";
const REFCOUNT_FILE: &str = "/data/refcount";
const REFCOUNT_LOCK: &str = "/data/refcount.lock";

const NIX_RW_DIRS: [&str; 3] = ["store", "var", "orb/data"];

fn unmount_wormhole() -> anyhow::Result<()> {
    // sometimes gets EINVAL = not mounted (??)
    // println!("unmounting unified/nix/store");
    // umount2("/mnt/wormhole-unified/nix/store",  MntFlags::MNT_DETACH)?;
    // println!("unmounting unified/nix/var");
    // umount2("/mnt/wormhole-unified/nix/var",  MntFlags::MNT_DETACH)?;
    // println!("unmounting unified/nix/orb/data");
    // umount2("/mnt/wormhole-unified/nix/orb/data", MntFlags::MNT_DETACH)?;
    println!("unmounting wormhole-unified");
    umount2("/mnt/wormhole-unified", MntFlags::empty())?;
    Ok(())
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

fn shutdown() -> anyhow::Result<()> {
    trace!("shutdown");
    let _flock = Flock::new_ofd(
        File::create(REFCOUNT_LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;

    let mut refcount: i32 = fs::read_to_string(REFCOUNT_FILE)?.trim().parse()?;
    refcount -= 1;

    trace!("updated refcount from {} to {}", refcount + 1, refcount);

    if refcount == 0 {
        unmount_wormhole()?;
    }

    fs::write(REFCOUNT_FILE, refcount.to_string())?;
    Ok(())
}

fn startup() -> anyhow::Result<()> {
    trace!("startup");
    OpenOptions::new()
        .create(true)
        .write(true)
        .append(true)
        .open(REFCOUNT_LOCK)?;
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

    if refcount == 0 {
        mount_wormhole()?;
    }
    refcount += 1;
    trace!("updated refcount from {} to {}", refcount - 1, refcount);

    fs::write(REFCOUNT_FILE, refcount.to_string())?;
    Ok(())
}

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    startup()?;
    println!("running driver");
    let config_str = std::env::args().nth(1).unwrap();
    let mut config = serde_json::from_str::<WormholeConfig>(&config_str)?;

    // see `doWormhole` in scon/ssh.go (~L300)
    let wormhole_mount = open_tree(
        "/mnt/wormhole-unified/nix",
        libc::OPEN_TREE_CLOEXEC as i32 | libc::OPEN_TREE_CLONE as i32 | libc::AT_RECURSIVE,
    )?;
    let (exit_code_pipe_read_fd, exit_code_pipe_write_fd) = pipe()?;
    let (log_pipe_read_fd, log_pipe_write_fd) = pipe()?;
    let wormhole_mount_fd = wormhole_mount.as_raw_fd();

    // disable cloexec for fd that we pass to wormhole-attach
    fcntl(
        wormhole_mount_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC,
        ),
    )?;
    fcntl(
        exit_code_pipe_write_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC,
        ),
    )?;
    fcntl(
        log_pipe_write_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC,
        ),
    )?;

    config.wormhole_mount_tree_fd = wormhole_mount_fd.as_raw_fd();
    config.exit_code_pipe_write_fd = exit_code_pipe_write_fd;
    config.drm_token = String::from("eyJhbGciOiJFZERTQSIsImtpZCI6IjEiLCJ0eXAiOiJKV1QifQ.eyJzdWIiOiIiLCJlbnQiOjEsImV0cCI6MiwiZW1nIjpudWxsLCJlc3QiOm51bGwsImF1ZCI6Im1hY3ZpcnQiLCJ2ZXIiOnsiY29kZSI6MTA3MDEwMCwiZ2l0IjoiZDRkNWY5NjAzZTYwZDQ3NDI2Yzg0ODhmODI3MTA0ZDY2MTlkNmY3YyJ9LCJkaWQiOiI3YmE5ZjA1ZDBlMGY2NTI3MjVkYzA3NjM5Y2VmYTg2NTM2ZWVlMmU5NTc4NDk2OWVlODcwZWMyZDY2YjEzMDI0IiwiaWlkIjoiYzdlYzY1M2FmZDljMDIxNjZlZjY2Nzc2MGVkYWNmODA0ZDc4OTlhZDE3YmQ1YWIxYzU4YzE4OGVjOGYxZTExYiIsImNpZCI6ImU1NjZiZjRiNmExNjNjYTM1NGU2OGQzYmU2ZjAzZDlmNzFkMzYxZTdhMmIxNjMzZDcwMzE0MmE2ODIwNmNjNDciLCJpc3MiOiJkcm1zZXJ2ZXIiLCJpYXQiOjE3MjczNzUwNjAsImV4cCI6MTcyNzk3OTg2MCwibmJmIjoxNzI3Mzc1MDYwLCJkdnIiOjEsIndhciI6MTcyNjk3MTM3MiwibHhwIjoxNzI3NTc2MTcyfQ.HpvQJJMmUJlIN-37KAYD-9hQKjW_Goarl0HR7h605UegBa-7LmBR3Fitn-jSHMt6-Yb5HPX0AZYjeOAIDSMpAA");

    config.log_fd = log_pipe_write_fd;

    let serialized = serde_json::to_string(&config)?;
    trace!("wormhole config: {}", serialized);
    match unsafe { fork()? } {
        ForkResult::Child => {
            trace!("starting wormhole-attach");

            execvp(
                &CString::new("./wormhole-attach")?,
                &[
                    CString::new("./wormhole-attach")?,
                    CString::new(serialized)?,
                ],
            )?;
            std::process::exit(0);
        }
        ForkResult::Parent { child } => {
            waitpid(child, None)?;
            let mut buffer = [0u8; mem::size_of::<i32>()]; // Buffer to hold 4 bytes (i32)
            read(exit_code_pipe_read_fd, &mut buffer)?;
            let num = i32::from_ne_bytes(buffer);
            trace!("wormhole-attach exit code: {}", num);
            shutdown()?;
        }
    }
    Ok(())
}

/*
locally:
kevin@orbstack macvirt % docker build --ssh default -t localhost:5000/wormhole-rootfs -f wormhole/remote/Dockerfile .
kevin@orbstack macvirt % docker push localhost:5000/wormhole-rootfs


on remote:
kevin@testremote:~$ docker pull 198.19.249.3:5000/wormhole-rootfs
kevin@testremote:~$ docker run -d --privileged --pid host --net host --cgroupns host -v wormhole-data:/data 198.19.249.3:5000/wormhole-rootfs

*/
