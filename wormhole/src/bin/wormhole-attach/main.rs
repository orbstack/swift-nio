use std::{collections::HashMap, ffi::CString, fs::File, os::fd::{AsRawFd, FromRawFd, OwnedFd}, path::Path, ptr::{null, null_mut}};

use libc::{prlimit, ptrace, sock_filter, sock_fprog, syscall, SYS_capset, SYS_seccomp, PR_CAPBSET_DROP, PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, PR_CAP_AMBIENT_RAISE, PTRACE_DETACH, PTRACE_EVENT_STOP, PTRACE_INTERRUPT, PTRACE_SEIZE};
use nix::{errno::Errno, fcntl::{open, openat, OFlag}, mount::{umount2, MntFlags, MsFlags}, sched::{setns, unshare, CloneFlags}, sys::{prctl, stat::{umask, Mode}, utsname::uname, wait::{waitpid, WaitStatus}}, unistd::{access, chdir, execve, fchown, fork, getpid, setgid, setgroups, setuid, AccessFlags, ForkResult, Gid, Pid, Uid}};
use pidfd::PidFd;
use tracing::{debug, error, span, trace, Level};
use tracing_subscriber::fmt::format::FmtSpan;
use wormhole::{err, flock::{Flock, FlockGuard, FlockMode, FlockWait}, newmount::{mount_setattr, move_mount, MountAttr, MOUNT_ATTR_RDONLY}};

use crate::proc::wait_for_exit;

mod drm;
mod pidfd;
mod proc;
mod subreaper;

const DIR_CREATE_LOCK: &str = "/dev/shm/.orb-wormhole-d.lock";

const EXTRA_ENV: &[(&str, &str)] = &[
    ("ZDOTDIR", "/nix/orb/sys/zsh"),
    ("LESSHISTFILE", "/nix/orb/data/home/.lesshst"),
    ("GIT_SSL_CAINFO", "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt"),
    ("NIX_SSL_CERT_FILE", "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt"),
    ("SSL_CERT_FILE", "/nix/orb/sys/etc/ssl/certs/ca-bundle.crt"),
    ("NIX_CONF_DIR", "/nix/orb/sys/etc"),
    // not needed: compiled into ncurses, but keep this for xterm-kitty
    ("TERMINFO_DIRS", "/nix/orb/data/.env-out/share/terminfo:/nix/orb/sys/share/terminfo"),
    ("NIX_PROFILES", "/nix/orb/data/.env-out"),
    ("XDG_DATA_DIRS", "/usr/local/share:/usr/share:/nix/orb/data/.env-out/share:/nix/orb/sys/share"),
    ("XDG_CONFIG_DIRS", "/etc/xdg:/nix/orb/data/.env-out/etc/xdg:/nix/orb/sys/etc/xdg"),
    //("MANPATH", "/nix/orb/data/.env-out/share/man:/nix/orb/sys/share/man"),
    // no NIX_PATH: we have no channels
    ("LIBEXEC_PATH", "/nix/orb/data/.env-out/libexec:/nix/orb/sys/libexec"),
    ("INFOPATH", "/nix/orb/data/.env-out/share/info:/nix/orb/sys/share/info"),
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
const INHERIT_ENVS: &[&str] = &[
    "TERM",
    "SSH_CONNECTION",
    "SSH_AUTH_SOCK",
];
const PREPEND_PATH: &str = "/nix/orb/data/.env-out/bin:/nix/orb/sys/bin";

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

fn mount_common(source: &str, dest: &str, fstype: Option<&str>, flags: MsFlags, data: Option<&str>) -> anyhow::Result<()> {
    nix::mount::mount(Some(source), dest, fstype, flags, data)?;
    Ok(())
}

fn copy_seccomp_filter(pid: i32, index: u32) -> anyhow::Result<()> {
    // attach via ptrace
    // SECCOMP_GET_FILTER requires ptrace-stop
    // safer way to stop (no signal races): PTRACE_SEIZE, then PTRACE_INTERRUPT
    trace!("seccomp: ptrace attach");
    unsafe { err(ptrace(PTRACE_SEIZE, pid, 0, 0))? };

    // then interrupt
    unsafe { err(ptrace(PTRACE_INTERRUPT, pid, 0, 0))? };

    // wait for it to enter ptrace-stop
    loop {
        let res = waitpid(Pid::from_raw(pid), None)?;
        match res {
            WaitStatus::PtraceEvent(_, _, PTRACE_EVENT_STOP) => break,
            _ => {}
        }
    }

    // get instruction count in seccomp filter
    trace!("seccomp: ptrace get filter size");
    let insn_count = unsafe { err(ptrace(PTRACE_SECCOMP_GET_FILTER.try_into().unwrap(), pid, index, null::<sock_filter>()))? };

    // dump filter
    trace!("seccomp: dump filter");
    let mut filter = vec![sock_filter {
        code: 0,
        jt: 0,
        jf: 0,
        k: 0,
    }; insn_count as usize];
    unsafe { err(ptrace(PTRACE_SECCOMP_GET_FILTER.try_into().unwrap(), pid, index, filter.as_mut_ptr() as *mut sock_filter))? };

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
    unsafe { err(syscall(SYS_seccomp, SECCOMP_SET_MODE_FILTER, 0, &fprog as *const sock_fprog))? };

    Ok(())
}

struct Mount {
    dest: String,
    flags: Vec<String>,
}

fn parse_proc_mounts(proc_mounts: &str) -> anyhow::Result<Vec<Mount>> {
    Ok(proc_mounts.lines()
        // skip empty lines
        .filter(|line| !line.is_empty())
        // get mount path
        .map(|line| {
            let mut iter = line.split_ascii_whitespace();
            let dest = iter.nth(1).unwrap().to_string();
            let flags = iter.nth(1).unwrap().split(',').map(|s| s.to_string()).collect();
            Mount {
                dest,
                flags,
            }
        })
        .collect())
}

fn is_root_readonly(proc_mounts: &[Mount]) -> bool {
    proc_mounts.iter()
        .any(|m| m.dest == "/" && m.flags.contains(&"ro".to_string()))
}

fn create_nix_dir(proc_mounts: &[Mount]) -> anyhow::Result<FlockGuard<()>> {
    trace!("create_nix_dir: wait for lock");
    let _flock = Flock::new_ofd(File::create(DIR_CREATE_LOCK)?, FlockMode::Exclusive, FlockWait::Blocking)?;
    match access("/nix", AccessFlags::F_OK) {
        Ok(_) => {
            // continue to lock
            trace!("create_nix_dir: already exists");
        },
        Err(Errno::ENOENT) => {
            // check attributes of '/' mount to deal with read-only containers
            let is_root_readonly = is_root_readonly(proc_mounts);
            if is_root_readonly {
                trace!("mounts: remount / as rw");
                mount_setattr(None, "/", 0, &MountAttr {
                    attr_set: 0,
                    attr_clr: MOUNT_ATTR_RDONLY,
                    propagation: 0,
                    userns_fd: 0,
                })?;
            }

            // use create_dir_all to avoid race with another cattach
            trace!("mounts: create /nix directory");
            std::fs::create_dir_all("/nix")?;

            // set xattr so we know to delete it later (i.e. we created it)
            trace!("mounts: set xattr on /nix");
            xattr::set("/nix", "user.orbstack.wormhole", b"1")?;

            if is_root_readonly {
                trace!("mounts: remount / as ro");
                mount_setattr(None, "/", 0, &MountAttr {
                    attr_set: MOUNT_ATTR_RDONLY,
                    attr_clr: 0,
                    propagation: 0,
                    userns_fd: 0,
                })?;
            }
        },
        Err(e) => return Err(e.into()),
    }

    // take a shared lock as a refcount
    trace!("create_nix_dir: take shared ref lock");
    let ref_lock = Flock::new_ofd(File::open("/nix")?, FlockMode::Shared, FlockWait::NonBlocking)?;
    Ok(FlockGuard::new(ref_lock, ()))
}

fn delete_nix_dir(proc_self_fd: &OwnedFd, nix_flock_ref: FlockGuard<()>) -> anyhow::Result<()> {
    // try to unmount everything on our view of /nix recursively
    let mounts_file = unsafe { File::from_raw_fd(openat(proc_self_fd.as_raw_fd(), "mounts", OFlag::O_RDONLY | OFlag::O_CLOEXEC, Mode::empty())?) };
    let proc_mounts = parse_proc_mounts(&std::io::read_to_string(mounts_file)?)?;
    for mnt in proc_mounts.iter().rev() {
        if mnt.dest == "/nix" || mnt.dest.starts_with("/nix/") {
            trace!("delete_nix_dir: unmount {}", mnt.dest);
            match umount2(Path::new(&mnt.dest), MntFlags::UMOUNT_NOFOLLOW) {
                Ok(_) => {}
                Err(Errno::EBUSY) => {
                    // still in use (bg / forked process)
                    trace!("delete_nix_dir: mounts still in use");
                    return Ok(());
                }
                Err(e) => return Err(e.into()),
            }
        }
    }

    trace!("delete_nix_dir: wait for lock");
    let _flock = Flock::new_ofd(File::create(DIR_CREATE_LOCK)?, FlockMode::Exclusive, FlockWait::Blocking)?;

    // drop our ref
    drop(nix_flock_ref);

    // check whether we created /nix
    if let None = xattr::get("/nix", "user.orbstack.wormhole")? {
        // we didn't create /nix, so don't delete it
        trace!("delete_nix_dir: /nix not created by us");
        return Ok(());
    }

    // check whether there are any remaining refs
    if Flock::check_ofd(File::open("/nix")?, FlockMode::Exclusive)? {
        // success - no refs; continue
        trace!("delete_nix_dir: no refs");
    } else {
        // there are still active refs, so we can't delete /nix
        trace!("delete_nix_dir: refs still active");
        return Ok(());
    }

    // good to go for deletion:
    // - we created it (according to xattr)
    // - no remaining refs (according to flock)

    // check attributes of '/' mount to deal with read-only containers
    let is_root_readonly = is_root_readonly(&proc_mounts);
    if is_root_readonly {
        trace!("mounts: remount / as rw");
        mount_setattr(None, "/", 0, &MountAttr {
            attr_set: 0,
            attr_clr: MOUNT_ATTR_RDONLY,
            propagation: 0,
            userns_fd: 0,
        })?;
    }

    trace!("delete_nix_dir: deleting /nix");
    std::fs::remove_dir("/nix")?;

    if is_root_readonly {
        trace!("mounts: remount / as ro");
        mount_setattr(None, "/", 0, &MountAttr {
            attr_set: MOUNT_ATTR_RDONLY,
            attr_clr: 0,
            propagation: 0,
            userns_fd: 0,
        })?;
    }

    Ok(())
}

// this is 75% of a container runtime, but a bit more complex... since it has to clone attributes of another process instead of just knowing what to set
// this does *not* include ALL process attributes like sched affinity, dumpable, securebits, etc. that docker doesn't set
fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_span_events(FmtSpan::CLOSE)
        .with_max_level(Level::TRACE)
        .init();

    // stdin, stdout, stderr are expected to be 0,1,2 and will be propagated to the child
    // usage: attach-ctr <init_pid> <container_workdir> <fd_fusebpf_mount_tree> <command> <drm_token>
    let init_pid = std::env::args().nth(1).unwrap().parse::<i32>()?;
    let container_workdir = std::env::args().nth(2).unwrap();
    let wormhole_mount_fd = unsafe { OwnedFd::from_raw_fd(std::env::args().nth(3).unwrap().parse::<i32>()?) };
    let entry_shell_cmd = std::env::args().nth(4).unwrap();
    let drm_token = std::env::args().nth(5).unwrap();
    trace!("entry shell cmd: {:?}", entry_shell_cmd);
    trace!("drm token: {:?}", drm_token);
    drm::verify_token(&drm_token)?;

    trace!("open pidfd");
    let pidfd = PidFd::open(init_pid)?;

    // easier to read everything here instead of keeping /proc/<pid> dirfd open for later
    trace!("read process info");
    let proc_status = std::fs::read_to_string(format!("/proc/{}/status", init_pid))?;
    let oom_score_adj = std::fs::read_to_string(format!("/proc/{}/oom_score_adj", init_pid))?;
    let proc_cgroup = std::fs::read_to_string(format!("/proc/{}/cgroup", init_pid))?;
    let proc_env = std::fs::read_to_string(format!("/proc/{}/environ", init_pid))?;
    let proc_mounts = parse_proc_mounts(&std::fs::read_to_string(format!("/proc/{}/mounts", init_pid))?)?;
    let num_caps = std::fs::read_to_string("/proc/sys/kernel/cap_last_cap")?.trim_end().parse::<u32>()? + 1;

    // prevent tracing
    prctl::set_dumpable(false)?;
    // prevent write-execute maps
    unsafe {
        libc::prctl(PR_SET_MDWE, PR_MDWE_REFUSE_EXEC_GAIN | PR_MDWE_NO_INHERIT, 0, 0, 0);
    }

    // copy before entering container mount ns
    // /proc/self link reads ENOENT if pidns of mount and current process don't match
    trace!("copy oom score adj");
    std::fs::write("/proc/self/oom_score_adj", oom_score_adj)?;

    // cgroupfs in mount ns is mounted in container's cgroupns
    {
        trace!("copy cgroup");
        let cg_path = proc_cgroup.lines().next().unwrap()
        .split(':')
        .last()
        .unwrap();
        let self_pid: i32 = getpid().into();
        std::fs::write(format!("/sys/fs/cgroup/{}/cgroup.procs", cg_path), format!("{}", self_pid))?;
    }

    // save dirfd of /proc/thread-self in old mount ns
    let proc_self_fd = unsafe { OwnedFd::from_raw_fd(open("/proc/thread-self", OFlag::O_PATH | OFlag::O_CLOEXEC, Mode::empty())?) };

    trace!("attach most namespaces");
    setns(&pidfd, CloneFlags::CLONE_NEWNS | CloneFlags::CLONE_NEWCGROUP | CloneFlags::CLONE_NEWUTS | CloneFlags::CLONE_NEWIPC | CloneFlags::CLONE_NEWNET)?;

    // set process name
    {
        let uname = uname()?;
        let hostname = uname.nodename().to_str().unwrap();
        proc::set_cmdline_name(&format!("orb-wormhole: container {}", hostname))?;
    }

    trace!("unshare mount ns");
    unshare(CloneFlags::CLONE_NEWNS)?;

    trace!("mounts: set propagation to private");
    mount_common("/", "/", None, MsFlags::MS_REC | MsFlags::MS_PRIVATE, None)?;

    // need to create /nix?
    let nix_flock_ref = create_nix_dir(&proc_mounts)?;

    // bind mount wormhole mount tree onto /nix
    trace!("mounts: bind mount wormhole mount tree onto /nix");
    move_mount(&wormhole_mount_fd, None, "/nix")?;

    trace!("set umask");
    umask(Mode::from_bits(0o022).unwrap());

    trace!("parse proc status info");
    let init_status = proc_status.lines()
        .map(|line| line.split_ascii_whitespace().collect::<Vec<_>>())
        // parse into key-value <&str, Vec<String>>
        .map(|line| (line[0], line.iter().skip(1).map(|s| s.to_string()).collect::<Vec<_>>()))
        .collect::<HashMap<_, _>>();

    // TODO: support setting uid
    trace!("copy uid/gid");
    let uid = 0;
    let uid: Uid = uid.into();
    let gid = init_status.get("Gid:").unwrap().get(0).unwrap();
    let gid: Gid = gid.parse::<u32>()?.into();
    fchown(0, Some(uid), Some(gid))?;
    fchown(1, Some(uid), Some(gid))?;
    fchown(2, Some(uid), Some(gid))?;
    setuid(uid.into())?;
    setgid(gid)?;

    trace!("copy supplementary groups");
    let groups = init_status.get("Groups:").unwrap();
    setgroups(&groups.iter()
        .map(|s| s.parse::<u32>().unwrap().into())
        .collect::<Vec<_>>())?;

    trace!("copy NO_NEW_PRIVS");
    let no_new_privs = init_status.get("NoNewPrivs:").unwrap().get(0).unwrap();
    if no_new_privs == "1" {
        prctl::set_no_new_privs()?;
    }

    // copy env
    trace!("copy env");
    // convert to HashMap, to allow for overriding
    let mut env_map = proc_env.split('\0')
        .map(|s| s.splitn(2, '=').collect::<Vec<&str>>())
        // skip invalid entries with no =
        .filter(|s| s.len() == 2)
        .map(|s| (s[0].to_string(), s[1].to_string()))
        .collect::<HashMap<_, _>>();
    // edit PATH (append and prepend)
    env_map.insert("PATH".to_string(), format!("{}:{}", PREPEND_PATH, env_map.get("PATH").unwrap_or(&"".to_string())));
    // append extra envs
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
    // convert back to CStrings
    let cstr_envs = env_map.iter()
        .map(|(k, v)| CString::new(format!("{}={}", k, v)))
        .collect::<anyhow::Result<Vec<_>, _>>()?;

    // close unnecessary fds
    drop(wormhole_mount_fd);

    trace!("fork into intermediate");
    match unsafe { fork()? } {
        // parent 1 = host monitor
        ForkResult::Parent { child } => {
            let _span = span!(Level::TRACE, "monitor").entered();

            // close unnecessary fds
            drop(pidfd);

            // wait until child (intermediate) exits
            trace!("waitpid");
            wait_for_exit(child)?;

            // try to delete /nix
            delete_nix_dir(&proc_self_fd, nix_flock_ref)?;
        }

        // child 1 = intermediate
        ForkResult::Child => {
            let _span = span!(Level::TRACE, "inter").entered();

            // kill self if parent (cattach waiter) dies
            proc::prctl_death_sig()?;

            // close lingering fds before user-controlled chdir
            drop(nix_flock_ref);
            drop(proc_self_fd);

            // then chdir to requested workdir (must do / first to avoid rel path vuln)
            // can fail (falls back to /)
            let target_workdir = if container_workdir.is_empty() {
                // copy cwd of init pid
                format!("/proc/{}/cwd", init_pid)
            } else {
                container_workdir
            };
            if let Err(e) = chdir(Path::new(&target_workdir)) {
                // fail silently. this happens when workdir doesn't exist
                debug!("failed to set working directory: {}", e);
            }

            // finish attaching

            trace!("attach remaining namespaces");
            // use a separate call to detect EINVAL on NEWUSER
            setns(&pidfd, CloneFlags::CLONE_NEWPID)?; // for child
            // entering current userns will return EINVAL. ignore that
            match setns(&pidfd, CloneFlags::CLONE_NEWUSER) {
                Ok(_) => {},
                Err(Errno::EINVAL) => trace!("set user ns failed with EINVAL, continuing"),
                Err(e) => return Err(e.into()),
            }
            drop(pidfd);

            trace!("copy rlimits");
            for &res in &[libc::RLIMIT_CPU, libc::RLIMIT_FSIZE, libc::RLIMIT_DATA, libc::RLIMIT_STACK, libc::RLIMIT_CORE, libc::RLIMIT_RSS, libc::RLIMIT_NPROC, libc::RLIMIT_NOFILE, libc::RLIMIT_MEMLOCK, libc::RLIMIT_AS, libc::RLIMIT_LOCKS, libc::RLIMIT_SIGPENDING, libc::RLIMIT_MSGQUEUE, libc::RLIMIT_NICE, libc::RLIMIT_RTPRIO, libc::RLIMIT_RTTIME] {
                let mut rlimit = libc::rlimit {
                    rlim_cur: 0,
                    rlim_max: 0,
                };
                // read init_pid's rlimit
                unsafe { err(prlimit(init_pid, res, null(), &mut rlimit))? };
                // write to self
                unsafe { err(prlimit(0, res, &rlimit, null_mut()))? };
            }

            // copy seccomp:
            // use ptrace + PTRACE_SECCOMP_GET_FILTER to dump BPF filters
            trace!("copy seccomp");
            let has_seccomp = init_status.get("Seccomp:").unwrap().get(0).unwrap() != "0";
            if has_seccomp {
                copy_seccomp_filter(init_pid, 0)?;
            }

            // copy capabilities
            // ptrace is actually allowed by default caps!
            // must be after seccomp: if we drop CAP_SYS_ADMIN and don't have NO_NEW_PRIVS, we can't set a seccomp filter
            // works because docker's seccomp filter allows capset/capget
            // order: ambient, bounding, effective, inheritable, permitted
            trace!("copy capabilities");
            let cap_inh = u64::from_str_radix(init_status.get("CapInh:").unwrap().get(0).unwrap(), 16)?;
            let cap_prm = u64::from_str_radix(init_status.get("CapPrm:").unwrap().get(0).unwrap(), 16)?;
            let cap_eff = u64::from_str_radix(init_status.get("CapEff:").unwrap().get(0).unwrap(), 16)?;
            let cap_bnd = u64::from_str_radix(init_status.get("CapBnd:").unwrap().get(0).unwrap(), 16)?;
            let cap_amb = u64::from_str_radix(init_status.get("CapAmb:").unwrap().get(0).unwrap(), 16)?;
            // ambient: clear all, then raise set caps
            trace!("copy capabilities: ambient");
            unsafe { err(libc::prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0))? };
            for i in 0..num_caps {
                if cap_amb & (1 << i) != 0 {
                    unsafe { err(libc::prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE, i as i32, 0, 0))? };
                }
            }
            // bounding: drop all unset caps
            trace!("copy capabilities: bounding");
            for i in 0..num_caps {
                if cap_bnd & (1 << i) == 0 {
                    unsafe { err(libc::prctl(PR_CAPBSET_DROP, i as i32, 0, 0, 0))? };
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
            unsafe { err(syscall(SYS_capset, &cap_user_hdr as *const CapUserHeader, &cap_user_data as *const CapUserData))? };

            // fork again...
            match unsafe { fork()? } {
                // parent 2 = intermediate (waiter)
                ForkResult::Parent { child } => {
                    trace!("loop");

                    // this process has no reason to keep existing.
                    // we only need to keep a monitor on the host, and subreaper in the pid ns
                    // once this exits, child (subreaper) will be reparented to host monitor in host pid ns
                    // TODO exit
                    // std::process::exit(0);
                    wait_for_exit(child)?;
                }

                // child 2 = subreaper
                ForkResult::Child => {
                    let _span = span!(Level::TRACE, "subreaper").entered();

                    // become subreaper, so children get a subreaper flag at fork time
                    prctl::set_child_subreaper(true)?;
                    // kill self if parent (cattach waiter) dies
                    proc::prctl_death_sig()?;

                    // fork again...
                    trace!("fork");
                    match unsafe { fork()? } {
                        // parent 2 = subreaper
                        ForkResult::Parent { child } => {
                            // subreaper helps us deal with zsh's zombie processes in any container where init is not a shell (e.g. distroless)
                            trace!("loop");
                            subreaper::run(child)?;
                        }

                        // child 2 = payload
                        ForkResult::Child => {
                            let _span = span!(Level::TRACE, "payload");

                            // kill self if parent (cattach subreaper) dies
                            // but allow bg processes to keep running
                            proc::prctl_death_sig()?;

                            trace!("execve");
                            execve(&CString::new("/nix/orb/sys/bin/dctl")?, &[CString::new("dctl")?, CString::new("__entrypoint")?, CString::new("--")?, CString::new(entry_shell_cmd)?], &cstr_envs)?;
                            unreachable!();
                        }
                    }
                }
            }
        }
    }

    Ok(())
}
