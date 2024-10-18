// ./driver (<config json> | --nuke) . --> runs wormhole-attach with the proper mount / fds

use nix::{
    errno::Errno,
    fcntl::{
        fcntl,
        FcntlArg::{self, F_GETFD},
        FdFlag,
    },
    mount::{self, mount, umount2, MntFlags, MsFlags},
    sys::wait::waitpid,
    unistd::{execve, execvp, execvpe, fork, pipe, read, ForkResult, ROOT},
};
use serde::{Deserialize, Serialize};
use std::{
    env,
    ffi::CString,
    fs::{self, read_to_string, File, OpenOptions},
    io, mem,
    os::fd::{AsRawFd, FromRawFd, RawFd},
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

fn unmount_wormhole() -> anyhow::Result<()> {
    for path in [
        "/mnt/wormhole-unified/nix/store",
        "/mnt/wormhole-unified/nix/var",
        "/mnt/wormhole-unified/nix/orb/data",
    ] {
        match umount2(path, MntFlags::MNT_DETACH) {
            Ok(_) => {}
            // ignore EINVAL, which happens if delete_nix_dir already unmounted the submount
            Err(Errno::EINVAL) => {}
            Err(err) => trace!("could not unmount {:?}", err),
        };
    }

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

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    let param = std::env::args().nth(1).unwrap();

    if param == "--nuke" {
        match fs::remove_dir_all(UPPERDIR) {
            Ok(_) => trace!("nuked remote data"),
            Err(e) => return Err(anyhow::anyhow!("error nuking data {:?}", e)),
        }
        return Ok(());
    }

    let mut config = serde_json::from_str::<WormholeConfig>(&param)?;

    startup()?;

    // see `doWormhole` in scon/ssh.go (~L300)
    let wormhole_mount = open_tree(
        "/mnt/wormhole-unified/nix",
        libc::OPEN_TREE_CLOEXEC as i32 | libc::OPEN_TREE_CLONE as i32 | libc::AT_RECURSIVE,
    )?;
    let (_, log_pipe_write_fd) = pipe()?;
    let wormhole_mount_fd = wormhole_mount.as_raw_fd();

    // disable cloexec for fd that we pass to wormhole-attach
    fcntl(
        wormhole_mount_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(wormhole_mount_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC,
        ),
    )?;
    fcntl(
        log_pipe_write_fd,
        FcntlArg::F_SETFD(
            FdFlag::from_bits_truncate(fcntl(log_pipe_write_fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC,
        ),
    )?;

    config.wormhole_mount_tree_fd = wormhole_mount_fd.as_raw_fd();
    config.log_fd = log_pipe_write_fd;
    config.exit_code_pipe_write_fd = -1;

    let serialized = serde_json::to_string(&config)?;
    trace!("wormhole config: {}", serialized);
    match unsafe { fork()? } {
        ForkResult::Child => {
            trace!("starting wormhole-attach");
            execvpe(
                &CString::new("./wormhole-attach")?,
                &[
                    CString::new("./wormhole-attach")?,
                    CString::new(serialized)?,
                ],
                &[CString::new("RUST_BACKTRACE=1")?],
            )?;
            unreachable!();
        }
        ForkResult::Parent { child } => {
            waitpid(child, None)?;
            // todo: shutdown even if processs is killed
            shutdown()?;
        }
    }
    Ok(())
}
