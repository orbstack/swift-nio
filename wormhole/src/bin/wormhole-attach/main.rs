use std::{
    borrow::Cow,
    collections::HashMap,
    ffi::CString,
    fs::File,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::Path,
    ptr::{null, null_mut},
};

use anyhow::anyhow;
use libc::{
    prlimit, ptrace, sock_filter, sock_fprog, syscall, SYS_capset, SYS_seccomp, OPEN_TREE_CLONE,
    PR_CAPBSET_DROP, PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, PR_CAP_AMBIENT_RAISE, PTRACE_DETACH,
    PTRACE_EVENT_STOP, PTRACE_INTERRUPT, PTRACE_SEIZE,
};
use mounts::with_remount_rw;
use nix::{
    errno::Errno,
    fcntl::{open, OFlag},
    mount::{umount2, MntFlags, MsFlags},
    sched::{setns, unshare, CloneFlags},
    sys::{
        prctl,
        signal::Signal,
        socket::{socketpair, AddressFamily, SockFlag, SockType},
        stat::{umask, Mode},
        utsname::uname,
        wait::{waitpid, WaitStatus},
    },
    unistd::{
        access, chdir, chroot, execve, fchown, fork, getpid, setgroups, setresgid, setresuid,
        AccessFlags, ForkResult, Gid, Pid, Uid,
    },
};

use pidfd::PidFd;
use signals::{mask_sigset, SigSet, SignalFd};
use tracing::{debug, span, trace, warn, Level};
use tracing_subscriber::fmt::format::FmtSpan;
use wormhole::{
    err,
    flock::{Flock, FlockMode, FlockWait},
    model::{WormholeConfig, WormholeRuntimeState},
    mount_common,
    newmount::{move_mount, open_tree},
    paths, set_cloexec,
};

mod drm;
mod monitor;
mod mounts;
mod pidfd;
mod proc;
mod signals;
mod subreaper;
mod subreaper_protocol;

const DIR_CREATE_LOCK: &str = "/dev/shm/.orb-wormhole-d.lock";
const PTRACE_LOCK: &str = "/dev/shm/.orb-wormhole-p.lock";

const EXTRA_ENV: &[(&str, &str)] = &[
    ("ZDOTDIR", "/nix/orb/sys/zsh"),
    ("LESSHISTFILE", "/nix/orb/data/home/.lesshst"),
    ("GIT_SSL_CAINFO", "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt"),
    (
        "NIX_SSL_CERT_FILE",
        "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt",
    ),
    ("SSL_CERT_FILE", "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt"),
    ("NIX_CONF_DIR", "/nix/orb/sys/etc"),
    // not needed: compiled into ncurses, but keep this for xterm-kitty
    (
        "TERMINFO_DIRS",
        "/nix/orb/data/.env-out/share/terminfo:/nix/orb/sys/share/terminfo",
    ),
    ("NIX_PROFILES", "/nix/orb/data/.env-out"),
    (
        "XDG_DATA_DIRS",
        "/usr/local/share:/usr/share:/nix/orb/data/.env-out/share:/nix/orb/sys/share",
    ),
    (
        "XDG_CONFIG_DIRS",
        "/etc/xdg:/nix/orb/data/.env-out/etc/xdg:/nix/orb/sys/etc/xdg",
    ),
    //("MANPATH", "/nix/orb/data/.env-out/share/man:/nix/orb/sys/share/man"),
    // no NIX_PATH: we have no channels
    (
        "LIBEXEC_PATH",
        "/nix/orb/data/.env-out/libexec:/nix/orb/sys/libexec",
    ),
    (
        "INFOPATH",
        "/nix/orb/data/.env-out/share/info:/nix/orb/sys/share/info",
    ),
    //("LESSKEYIN_SYSTEM", "/nix/store/jsyxjk9lcrvncmnpjghlp0ar258z3rdy-lessconfig"),

    // fixes nixos + zsh bug with duplicated chars in prompt after tab completion
    // https://github.com/nix-community/home-manager/issues/3711
    ("LANG", "C.UTF-8"),
    // not set by scon because user=""
    ("USER", "root"),
    // e.g. for ~/.config/htop/htoprc
    ("XDG_CONFIG_HOME", "/nix/orb/data/home/.config"),
    // for nix and other programs, incl. .zsh_history
    ("XDG_CACHE_HOME", "/nix/orb/data/home/.cache"),
];
const INHERIT_ENVS: &[&str] = &["TERM", "SSH_CONNECTION", "SSH_AUTH_SOCK"];
// there's no generic solution for problematic env vars...
// but we need to inherit env for many apps to work correctly
const EXCLUDE_ENVS: &[&str] = &[
    // LD_PRELOAD libs may depend on musl (which fails to load in nix rpath) or conflicting glibc
    // checking the lib's DT_NEEDED is pointless: impossible to have a statically-linked dynamic lib
    // https://github.com/orbstack/orbstack/issues/1131
    "LD_PRELOAD",
    // BASH_ENV is basically bashrc, but for *all* bash instances, including #! shell scripts
    // nix has binaries like "manpath" that are bash wrapper scripts
    // if BASH_ENV script (e.g. bashrc -> nvm) runs such commands (e.g. manpath), we get infinite recursion
    // https://github.com/orbstack/orbstack/issues/1096
    "BASH_ENV",
];
const PREPEND_PATH: &str = "/nix/orb/sys/bin";
const APPEND_PATH: &str = "/nix/orb/data/.env-out/bin";

// type mismatch: musl=c_int, glibc=c_uint
const PTRACE_SECCOMP_GET_FILTER: libc::c_uint = 0x420c;

// musl is missing this
const SECCOMP_SET_MODE_FILTER: libc::c_uint = 1;

/* V3 added in Linux 2.6.26 */
const _LINUX_CAPABILITY_VERSION_3: u32 = 0x20080522;
const _LINUX_CAPABILITY_U32S_3: u32 = 2;

// kernel 6.3
const PR_SET_MDWE: i32 = 65;
const PR_MDWE_REFUSE_EXEC_GAIN: i32 = 1;
const PR_MDWE_NO_INHERIT: i32 = 2;

#[repr(C)]
struct CapUserHeader {
    version: u32,
    pid: i32,
}

#[repr(C)]
struct CapUserData {
    // datap[0]
    effective_lo: u32,
    permitted_lo: u32,
    inheritable_lo: u32,
    // datap[1]
    effective_hi: u32,
    permitted_hi: u32,
    inheritable_hi: u32,
}

fn copy_seccomp_filter(pid: i32, index: u32) -> anyhow::Result<()> {
    // need to lock or we can race and EPERM
    let _flock = Flock::new_ofd(
        File::create(PTRACE_LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;

    // attach via ptrace
    // SECCOMP_GET_FILTER requires ptrace-stop
    // safer way to stop (no signal races): PTRACE_SEIZE, then PTRACE_INTERRUPT
    trace!("seccomp: ptrace attach");
    unsafe { err(ptrace(PTRACE_SEIZE, pid, 0, 0))? };

    // then interrupt
    unsafe { err(ptrace(PTRACE_INTERRUPT, pid, 0, 0))? };

    // wait for it to enter ptrace-stop
    loop {
        trace!("waitpid...");
        let res = waitpid(Pid::from_raw(pid), None)?;
        trace!("waitpid: {:?}", res);
        match res {
            // impossible because entire pid ns will be killed if pid 1 exits
            WaitStatus::Exited(_, _) | WaitStatus::Signaled(_, _, _) => {
                return Err(anyhow!("process exited"))
            }
            // common case
            WaitStatus::PtraceEvent(_, _, PTRACE_EVENT_STOP) => break,
            // if also stopped by a signal (e.g. SIGILL)
            WaitStatus::Stopped(_, _) => break,
            // impossible because we didn't enable syscall tracing, but it does count as a stop in case someone else is tracing this process...
            WaitStatus::PtraceSyscall(_) => break,
            _ => {}
        }
    }

    // get instruction count in seccomp filter
    trace!("seccomp: ptrace get filter size");
    let insn_count = unsafe {
        err(ptrace(
            // cast needed to fix musl type mismatch
            PTRACE_SECCOMP_GET_FILTER as _,
            pid,
            index,
            null::<sock_filter>(),
        ))?
    };

    // dump filter
    trace!("seccomp: dump filter");
    let mut filter = vec![
        sock_filter {
            code: 0,
            jt: 0,
            jf: 0,
            k: 0,
        };
        insn_count as usize
    ];
    unsafe {
        err(ptrace(
            // cast needed to fix musl type mismatch
            PTRACE_SECCOMP_GET_FILTER as _,
            pid,
            index,
            filter.as_mut_ptr(),
        ))?
    };

    // detach ptrace
    trace!("seccomp: detach ptrace");
    unsafe { err(ptrace(PTRACE_DETACH, pid, 0, 0))? };

    // create sock_fprog
    let fprog = sock_fprog {
        len: insn_count as u16,
        filter: filter.as_mut_ptr(),
    };
    // set filter
    trace!("seccomp: set filter");
    unsafe {
        err(syscall(
            SYS_seccomp,
            SECCOMP_SET_MODE_FILTER,
            0,
            &fprog as *const sock_fprog,
        ))?
    };

    Ok(())
}

struct Mount {
    dest: String,
}

fn parse_proc_mounts(proc_mounts: &str) -> anyhow::Result<Vec<Mount>> {
    Ok(proc_mounts
        .lines()
        // skip empty lines
        .filter(|line| !line.is_empty())
        // get mount path
        .map(|line| {
            let mut iter = line.split_ascii_whitespace();
            let dest = iter.nth(1).unwrap().to_string();
            Mount { dest }
        })
        .collect())
}

fn create_nix_dir() -> anyhow::Result<Flock> {
    trace!("create_nix_dir: wait for lock");
    let _flock = Flock::new_ofd(
        File::create(DIR_CREATE_LOCK)?,
        FlockMode::Exclusive,
        FlockWait::Blocking,
    )?;
    match access("/nix", AccessFlags::F_OK) {
        Ok(_) => {
            // continue to lock
            trace!("create_nix_dir: already exists");
        }
        Err(Errno::ENOENT) => {
            trace!("mounts: create /nix directory");

            with_remount_rw(|| {
                // use create_dir_all to avoid races with other attachers
                std::fs::create_dir_all("/nix")?;

                // set xattr so we know to delete it later (i.e. we created it)
                // do this even if EEXIST, in case we exit and delete it before the other attacher sets xattr
                trace!("mounts: set xattr on /nix");
                xattr::set("/nix", "user.orbstack.wormhole", b"1")?;

                Ok(())
            })?;
        }
        Err(e) => return Err(e.into()),
    }

    // take a shared lock as a refcount
    trace!("create_nix_dir: take shared ref lock");
    let ref_lock = Flock::new_ofd(
        File::open("/nix")?,
        FlockMode::Shared,
        FlockWait::NonBlocking,
    )?;
    Ok(ref_lock)
}

fn switch_rootfs(rootfs_fd: &OwnedFd, proc_mounts: &[Mount]) -> anyhow::Result<()> {
    // in writable temp container rootfs, make a temp dir for mounting
    std::fs::create_dir_all("/mnttmp")?;

    // mount new rootfs at /mnttmp
    trace!("mounts: mount new rootfs on /mnttmp");
    move_mount(Some(rootfs_fd), None, None, Some("/mnttmp"))?;

    // move all mounts over to new rootfs
    for mount in proc_mounts {
        if mount.dest == "/" {
            continue;
        }

        trace!("mounts: move {:?}", &mount.dest);
        let tree_fd = open_tree(&mount.dest, OPEN_TREE_CLONE)?;
        match move_mount(
            Some(&tree_fd),
            None,
            Some(rootfs_fd),
            Some(&("/mnttmp/".to_string() + mount.dest.strip_prefix("/").unwrap())),
        ) {
            Ok(_) => {}
            Err(e) => {
                warn!("move_mount failed: {}", e);
            }
        }
    }

    // pivot to new rootfs
    trace!("mounts: pivot");
    chroot("/mnttmp")?;
    chdir("/")?;

    Ok(())
}

fn parse_config() -> anyhow::Result<(WormholeConfig, WormholeRuntimeState)> {
    let config_str = std::env::args().nth(1).unwrap();
    let runtime_state_str = std::env::args().nth(2).unwrap();
    let config = serde_json::from_str(&config_str)?;
    let runtime_state = serde_json::from_str(&runtime_state_str)?;
    Ok((config, runtime_state))
}

/*
 * minimum kernel req: 6.1 (latest stable debian 12 bookworm)
 */
// this is 75% of a container runtime, but a bit more complex... since it has to clone attributes of another process instead of just knowing what to set
// this does *not* include ALL process attributes like sched affinity, dumpable, securebits, etc. that docker doesn't set
fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    // stdin, stdout, stderr are expected to be 0,1,2 and will be propagated to the child
    // usage: wormhole-attach <config json>
    let (config, runtime_state) = parse_config()?;
    let rootfs_fd = runtime_state
        .rootfs_fd
        .map(|fd| unsafe { OwnedFd::from_raw_fd(fd) });
    // exit_code_pipe_write_fd needs to be leaked in monitor and subreaper to avoid closing on panic, which causes immediate SIGPWR
    let log_fd = unsafe { OwnedFd::from_raw_fd(runtime_state.log_fd) };
    let wormhole_mount_fd = unsafe { OwnedFd::from_raw_fd(runtime_state.wormhole_mount_tree_fd) };
    let exit_code_pipe_write_fd =
        unsafe { OwnedFd::from_raw_fd(runtime_state.exit_code_pipe_write_fd) };
    trace!("entry shell cmd: {:?}", config.entry_shell_cmd);
    trace!("drm token: {:?}", config.drm_token);
    drm::verify_token(&config.drm_token)?;

    // set cloexec on extra files passed to us
    if let Some(ref rootfs_fd) = rootfs_fd {
        set_cloexec(rootfs_fd.as_raw_fd())?;
    }
    set_cloexec(exit_code_pipe_write_fd.as_raw_fd())?;
    set_cloexec(log_fd.as_raw_fd())?;
    set_cloexec(wormhole_mount_fd.as_raw_fd())?;

    // set sigpipe
    {
        let mut set = SigSet::empty()?;
        set.add_signal(Signal::SIGPIPE as i32)?;
        mask_sigset(&set, libc::SIG_BLOCK)?;
    }

    trace!("open pidfd");
    let pidfd = PidFd::open(config.init_pid)?;

    // easier to read everything here instead of keeping /proc/<pid> dirfd open for later
    trace!("read process info");
    let proc_status = std::fs::read_to_string(format!("/proc/{}/status", config.init_pid))?;
    let oom_score_adj =
        std::fs::read_to_string(format!("/proc/{}/oom_score_adj", config.init_pid))?;
    let proc_cgroup = std::fs::read_to_string(format!("/proc/{}/cgroup", config.init_pid))?;
    let proc_env = std::fs::read_to_string(format!("/proc/{}/environ", config.init_pid))?;
    let proc_mounts = parse_proc_mounts(&std::fs::read_to_string(format!(
        "/proc/{}/mounts",
        config.init_pid
    ))?)?;
    let num_caps = std::fs::read_to_string("/proc/sys/kernel/cap_last_cap")?
        .trim_end()
        .parse::<u32>()?
        + 1;

    // prevent tracing
    // also helps with CVE-2019-5736: affects /proc/pid/exe permissions
    prctl::set_dumpable(false)?;

    // prevent write-execute maps
    unsafe {
        libc::prctl(
            PR_SET_MDWE,
            PR_MDWE_REFUSE_EXEC_GAIN | PR_MDWE_NO_INHERIT,
            0,
            0,
            0,
        );
    }

    // copy before entering container mount ns
    // /proc/self link reads ENOENT if pidns of mount and current process don't match
    trace!("copy oom score adj");
    std::fs::write("/proc/self/oom_score_adj", oom_score_adj)?;

    // cgroupfs in mount ns is mounted in container's cgroupns
    trace!("copy cgroup");
    let cgroup_path = proc_cgroup
        .lines()
        .next()
        .unwrap()
        .split(':')
        .last()
        .unwrap();
    let self_pid: i32 = getpid().into();
    std::fs::write(
        format!("/sys/fs/cgroup/{}/cgroup.procs", cgroup_path),
        format!("{}", self_pid),
    )?;

    // save dirfd of /proc
    let proc_fd = unsafe {
        OwnedFd::from_raw_fd(open(
            "/proc",
            OFlag::O_PATH | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };

    trace!("attach most namespaces");
    setns(
        &pidfd,
        CloneFlags::CLONE_NEWNS
            | CloneFlags::CLONE_NEWCGROUP
            | CloneFlags::CLONE_NEWUTS
            | CloneFlags::CLONE_NEWIPC
            | CloneFlags::CLONE_NEWNET,
    )?;

    // set process name
    {
        let uname = uname()?;
        let hostname = uname.nodename().to_str().unwrap();
        proc::set_cmdline_name(&format!("orb-wormhole: container {}", hostname))?;
    }

    // unmount wormhole stub bind mount in ephemeral container ns
    if rootfs_fd.is_some() {
        trace!("unmount wormhole stub bind mount in container ns");
        if let Err(e) = umount2("/dev/shm/.orb-wormhole-stub", MntFlags::MNT_DETACH) {
            debug!("unmount wormhole stub bind mount failed: {}", e);
        }
    }

    trace!("unshare mount ns");
    unshare(CloneFlags::CLONE_NEWNS)?;

    trace!("mounts: set propagation to private");
    mount_common("/", "/", None, MsFlags::MS_REC | MsFlags::MS_PRIVATE, None)?;

    // switch rootfs if needed
    if let Some(ref rootfs_fd) = rootfs_fd {
        switch_rootfs(rootfs_fd, &proc_mounts)?;
    }

    // unmount wormhole stub bind mount entirely in wormhole ns
    if rootfs_fd.is_some() {
        trace!("unmount wormhole stub bind mount in new ns");
        if let Err(e) = umount2("/dev/shm/.orb-wormhole-stub", MntFlags::MNT_DETACH) {
            debug!("unmount wormhole stub bind mount failed: {}", e);
        }
    }

    drop(rootfs_fd);

    // need to create /nix?
    let nix_flock_ref = create_nix_dir()?;

    // bind mount wormhole mount tree onto /nix
    trace!("mounts: bind mount wormhole mount tree onto /nix");
    move_mount(Some(&wormhole_mount_fd), None, None, Some("/nix"))?;

    trace!("set umask");
    umask(Mode::from_bits(0o022).unwrap());

    trace!("parse proc status info");
    let init_status = proc_status
        .lines()
        .map(|line| line.split_ascii_whitespace().collect::<Vec<_>>())
        // parse into key-value <&str, Vec<String>>
        .map(|line| {
            (
                line[0],
                line.iter()
                    .skip(1)
                    .map(|s| s.to_string())
                    .collect::<Vec<_>>(),
            )
        })
        .collect::<HashMap<_, _>>();

    trace!("chown stdio fds");
    let uid = "0";
    let uid: Uid = uid.parse::<u32>()?.into();
    let gid = init_status.get("Gid:").unwrap().first().unwrap();
    let gid: Gid = gid.parse::<u32>()?.into();
    fchown(0, Some(uid), Some(gid))?;
    fchown(1, Some(uid), Some(gid))?;
    fchown(2, Some(uid), Some(gid))?;

    trace!("copy supplementary groups");
    let groups = init_status.get("Groups:").unwrap();
    setgroups(
        &groups
            .iter()
            .map(|s| s.parse::<u32>().unwrap().into())
            .collect::<Vec<_>>(),
    )?;

    trace!("copy NO_NEW_PRIVS");
    let no_new_privs = init_status.get("NoNewPrivs:").unwrap().first().unwrap();
    if no_new_privs == "1" {
        prctl::set_no_new_privs()?;
    }

    // copy env
    trace!("copy env");
    // start with container's env, then override with current /proc env
    // convert to HashMap for easy overriding
    let mut env_map = config
        .container_env
        .as_ref()
        .map(Cow::Borrowed)
        .unwrap_or_else(|| Cow::Owned(Vec::new()))
        .iter()
        .map(|s| s.as_str())
        // chain with /proc, which is &str
        .chain(proc_env.split('\0'))
        .map(|s| s.splitn(2, '=').collect::<Vec<&str>>())
        // skip invalid entries with no =
        .filter(|s| s.len() == 2)
        .map(|s| (s[0].to_string(), s[1].to_string()))
        // exclude envs that are known to cause issues
        .filter(|(k, _)| !EXCLUDE_ENVS.contains(&k.as_str()))
        .collect::<HashMap<_, _>>();
    // edit PATH (append and prepend)
    env_map.insert(
        "PATH".to_string(),
        format!(
            "{}:{}:{}",
            PREPEND_PATH,
            env_map.get("PATH").unwrap_or(&"".to_string()),
            APPEND_PATH,
        ),
    );
    // set/overwrite extra envs
    env_map.reserve(EXTRA_ENV.len() + INHERIT_ENVS.len());
    for (k, v) in EXTRA_ENV {
        env_map.insert(k.to_string(), v.to_string());
    }
    // inherit some envs from ssh client
    for &key in INHERIT_ENVS {
        if let Ok(val) = std::env::var(key) {
            env_map.insert(key.to_string(), val.to_string());
        }
    }
    // set SHELL
    env_map.insert("SHELL".to_string(), paths::SHELL.to_string());

    // close unnecessary fds
    drop(wormhole_mount_fd);

    // monitor needs to be a subreaper so that it will get the container subreaper when intermediate exits
    prctl::set_child_subreaper(true)?;

    // parent needs to know when subreaper exits (after being reparented) and when wormhole signals it
    let monitor_sfd = {
        let mut set = SigSet::empty()?;

        set.add_signal(Signal::SIGCHLD as i32)?;

        // forwarded signals
        set.add_signal(Signal::SIGABRT as i32)?;
        set.add_signal(Signal::SIGALRM as i32)?;
        set.add_signal(Signal::SIGHUP as i32)?;
        set.add_signal(Signal::SIGINT as i32)?;
        set.add_signal(Signal::SIGQUIT as i32)?;
        set.add_signal(Signal::SIGTERM as i32)?;
        set.add_signal(Signal::SIGUSR1 as i32)?;
        set.add_signal(Signal::SIGUSR2 as i32)?;
        set.add_signal(Signal::SIGPWR as i32)?;

        mask_sigset(&set, libc::SIG_BLOCK)?;

        SignalFd::new(&set, libc::SFD_CLOEXEC | libc::SFD_NONBLOCK)?
    };

    // this pipe lets us communicate with the subreaper
    let (subreaper_socket_fd, monitor_socket_fd) = socketpair(
        AddressFamily::Unix,
        SockType::Stream,
        None,
        SockFlag::SOCK_CLOEXEC,
    )?;

    trace!("fork into intermediate");
    // SAFE: we're single-threaded so malloc and locks after fork are ok
    match unsafe { fork()? } {
        // parent 1 = host monitor
        ForkResult::Parent {
            child: intermediate,
        } => {
            let _span = span!(Level::TRACE, "monitor").entered();

            // close unnecessary fds
            drop(subreaper_socket_fd);
            drop(pidfd);

            trace!("running monitor");
            monitor::run(
                &runtime_state,
                proc_fd,
                nix_flock_ref,
                monitor_socket_fd,
                cgroup_path,
                intermediate,
                monitor_sfd,
            )?;
        }

        // child 1 = intermediate
        ForkResult::Child => {
            let _span = span!(Level::TRACE, "inter").entered();
            trace!("hello from child");

            // kill self if parent (cattach waiter) dies
            proc::prctl_death_sig()?;

            // close lingering fds before user-controlled chdir
            drop(nix_flock_ref);
            drop(proc_fd);
            drop(monitor_socket_fd);
            drop(monitor_sfd);

            // then chdir to requested workdir (must do / first to avoid rel path vuln)
            // can fail (falls back to /)
            let target_workdir = config.container_workdir.clone().unwrap_or_else(|| {
                // copy cwd of init pid
                format!("/proc/{}/cwd", config.init_pid)
            });
            if let Err(e) = chdir(Path::new(&target_workdir)) {
                // fail silently. this happens when workdir doesn't exist
                debug!("failed to set working directory: {}", e);
                env_map.insert("PWD".to_string(), "/".to_string());
            } else {
                env_map.insert("PWD".to_string(), target_workdir);
            }

            // finish attaching

            trace!("attach remaining namespaces");
            // use a separate call to detect EINVAL on NEWUSER
            setns(&pidfd, CloneFlags::CLONE_NEWPID)?; // for child
                                                      // entering current userns will return EINVAL. ignore that
            match setns(&pidfd, CloneFlags::CLONE_NEWUSER) {
                Ok(_) => {}
                Err(Errno::EINVAL) => trace!("set user ns failed with EINVAL, continuing"),
                Err(e) => return Err(e.into()),
            }
            drop(pidfd);

            trace!("copy rlimits");
            for &res in &[
                libc::RLIMIT_CPU,
                libc::RLIMIT_FSIZE,
                libc::RLIMIT_DATA,
                libc::RLIMIT_STACK,
                libc::RLIMIT_CORE,
                libc::RLIMIT_RSS,
                libc::RLIMIT_NPROC,
                libc::RLIMIT_NOFILE,
                libc::RLIMIT_MEMLOCK,
                libc::RLIMIT_AS,
                libc::RLIMIT_LOCKS,
                libc::RLIMIT_SIGPENDING,
                libc::RLIMIT_MSGQUEUE,
                libc::RLIMIT_NICE,
                libc::RLIMIT_RTPRIO,
                libc::RLIMIT_RTTIME,
            ] {
                let mut rlimit = libc::rlimit {
                    rlim_cur: 0,
                    rlim_max: 0,
                };
                // read init_pid's rlimit
                unsafe { err(prlimit(config.init_pid, res, null(), &mut rlimit))? };
                // write to self
                unsafe { err(prlimit(0, res, &rlimit, null_mut()))? };
            }

            // copy seccomp:
            // use ptrace + PTRACE_SECCOMP_GET_FILTER to dump BPF filters
            trace!("copy seccomp");
            let has_seccomp = init_status.get("Seccomp:").unwrap().first().unwrap() != "0";
            if has_seccomp {
                copy_seccomp_filter(config.init_pid, 0)?;
            }

            // keep capabilities across UID transition
            // (gets reset on execve)
            prctl::set_keepcaps(true)?;

            // bounding: drop all unset caps
            // this requires CAP_SETCAP so do it before we lose eff caps
            trace!("copy capabilities: bounding");
            let cap_bnd =
                u64::from_str_radix(init_status.get("CapBnd:").unwrap().first().unwrap(), 16)?;
            for i in 0..num_caps {
                if cap_bnd & (1 << i) == 0 {
                    unsafe { err(libc::prctl(PR_CAPBSET_DROP, i as i32, 0, 0, 0))? };
                }
            }

            // copy real uid, effective uid, saved uid
            trace!("copy uid/gid");
            setresgid(gid, gid, gid)?;
            setresuid(uid, uid, uid)?;

            // copy remaining capabilities
            // ptrace is actually allowed by default caps!
            // must be after seccomp: if we drop CAP_SYS_ADMIN and don't have NO_NEW_PRIVS, we can't set a seccomp filter
            // works because docker's seccomp filter allows capset/capget
            // order: ambient, bounding, effective, inheritable, permitted
            trace!("copy remaining capabilities");
            let cap_inh =
                u64::from_str_radix(init_status.get("CapInh:").unwrap().first().unwrap(), 16)?;
            let cap_prm =
                u64::from_str_radix(init_status.get("CapPrm:").unwrap().first().unwrap(), 16)?;
            let cap_eff =
                u64::from_str_radix(init_status.get("CapEff:").unwrap().first().unwrap(), 16)?;
            let cap_amb =
                u64::from_str_radix(init_status.get("CapAmb:").unwrap().first().unwrap(), 16)?;
            // ambient: clear all, then raise set caps
            trace!("copy capabilities: ambient");
            unsafe {
                err(libc::prctl(
                    PR_CAP_AMBIENT,
                    PR_CAP_AMBIENT_CLEAR_ALL,
                    0,
                    0,
                    0,
                ))?
            };
            for i in 0..num_caps {
                if cap_amb & (1 << i) != 0 {
                    unsafe {
                        err(libc::prctl(
                            PR_CAP_AMBIENT,
                            PR_CAP_AMBIENT_RAISE,
                            i as i32,
                            0,
                            0,
                        ))?
                    };
                }
            }
            // set eff/prm/inh
            trace!("copy capabilities: effective/permitted/inheritable");
            let cap_user_hdr = CapUserHeader {
                version: _LINUX_CAPABILITY_VERSION_3,
                pid: 0,
            };
            let cap_user_data = CapUserData {
                effective_lo: (cap_eff & 0xffffffff) as u32,
                permitted_lo: (cap_prm & 0xffffffff) as u32,
                inheritable_lo: (cap_inh & 0xffffffff) as u32,
                effective_hi: (cap_eff >> 32) as u32,
                permitted_hi: (cap_prm >> 32) as u32,
                inheritable_hi: (cap_inh >> 32) as u32,
            };
            unsafe {
                err(syscall(
                    SYS_capset,
                    &cap_user_hdr as *const CapUserHeader,
                    &cap_user_data as *const CapUserData,
                ))?
            };

            let subreaper_sfd = {
                let mut set = SigSet::empty()?;
                set.add_signal(Signal::SIGCHLD as i32)?;
                // keep sigpipe masked
                set.add_signal(Signal::SIGPIPE as i32)?;
                mask_sigset(&set, libc::SIG_SETMASK)?;
                SignalFd::new(&set, libc::SFD_CLOEXEC | libc::SFD_NONBLOCK)?
            };

            // fork again...
            match unsafe { fork() } {
                // parent 2 = intermediate (waiter)
                Ok(ForkResult::Parent { child: _ }) => {
                    trace!("intermediate dying");

                    // this process has no reason to keep existing.
                    // we only need to keep a monitor on the host, and subreaper in the pid ns
                    // once this exits, child (subreaper) will be reparented to host monitor in host pid ns
                    std::process::exit(0);
                }

                // child 2 = subreaper
                Ok(ForkResult::Child) => {
                    let _span = span!(Level::TRACE, "subreaper").entered();

                    // become subreaper, so children get a subreaper flag at fork time
                    prctl::set_child_subreaper(true)?;

                    // fork again...
                    trace!("fork");
                    match unsafe { fork()? } {
                        // parent 2 = subreaper
                        ForkResult::Parent { child } => {
                            // subreaper helps us deal with zsh's zombie processes in any container where init is not a shell (e.g. distroless)

                            subreaper::run(
                                exit_code_pipe_write_fd,
                                log_fd,
                                subreaper_socket_fd,
                                subreaper_sfd,
                                child,
                            )?;
                            trace!("subreaper exited");
                        }

                        // child 2 = payload
                        ForkResult::Child => {
                            let _span = span!(Level::TRACE, "payload");

                            // clear our masked signals
                            mask_sigset(&SigSet::empty()?, libc::SIG_SETMASK)?;

                            // die when subreaper dies
                            proc::prctl_death_sig()?;

                            let shell_cmd =
                                config.entry_shell_cmd.unwrap_or_else(|| "".to_string());
                            let cstr_envs = env_map
                                .iter()
                                .map(|(k, v)| CString::new(format!("{}={}", k, v)))
                                .collect::<anyhow::Result<Vec<_>, _>>()?;

                            trace!("execve {shell_cmd}");
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
                    }
                }

                Err(Errno::ENOMEM) => {
                    // ENOMEM = forked into dying pid ns (pid 1 exited)
                    // this means we raced with a stopping container
                    eprintln!("Container stopped.\nEnding Debug Shell session.");
                    std::process::exit(0);
                }

                Err(e) => return Err(e.into()),
            }
        }
    }

    Ok(())
}
