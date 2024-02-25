use std::{ffi::CString, os::fd::{AsRawFd, FromRawFd, OwnedFd}, path::Path, ptr::{null, null_mut}};

use libc::{prlimit, ptrace, sock_filter, sock_fprog, syscall, waitpid, SYS_capset, SYS_seccomp, AT_RECURSIVE, OPEN_TREE_CLOEXEC, OPEN_TREE_CLONE, PR_CAPBSET_DROP, PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, PR_CAP_AMBIENT_RAISE, PTRACE_ATTACH, PTRACE_DETACH};
use nix::{errno::Errno, fcntl::{openat, OFlag}, mount::MsFlags, sched::{setns, unshare, CloneFlags}, sys::{prctl, stat::{umask, Mode}}, unistd::{chdir, chroot, execve, fchdir, fork, getpid, setgid, setgroups, ForkResult, Gid}};
use pidfd::PidFd;
use tracing::{trace, Level};
use tracing_subscriber::fmt::format::FmtSpan;
use wormholefs::newmount::{move_mount, open_tree};

mod pidfd;

const EXTRA_ENV: &[&str] = &[
    "ZDOTDIR=/nix",
    // TODO: fill in this path
    "GIT_SSL_CAINFO=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt",
    "NIX_SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt",
    "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt",
];
const INHERIT_ENVS: &[&str] = &[
    "TERM",
    "SSH_CONNECTION",
    "SSH_AUTH_SOCK",
];
const APPEND_PATH: &str = "/nix/bin";

// type mismatch: musl=c_int, glibc=c_uint
const PTRACE_SECCOMP_GET_FILTER: libc::c_uint = 0x420c;

// musl is missing this
const SECCOMP_SET_MODE_FILTER: libc::c_uint = 1;

/* V3 added in Linux 2.6.26 */
const _LINUX_CAPABILITY_VERSION_3: u32 = 0x20080522;
const _LINUX_CAPABILITY_U32S_3: u32 = 2;

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
    // this stops the process for a bit, but SECCOMP_GET_FILTER doesn't work with a SEIZEd process
    trace!("seccomp: ptrace attach");
    let ret = unsafe { ptrace(PTRACE_ATTACH, pid, 0, 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // wait for ptrace to attach
    let ret = unsafe { waitpid(pid, null_mut(), 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // get instruction count in seccomp filter
    trace!("seccomp: ptrace get filter size");
    let insn_count = unsafe { ptrace(PTRACE_SECCOMP_GET_FILTER.try_into().unwrap(), pid, index, null::<sock_filter>()) };
    if insn_count < 0 {
        return Err(Errno::last().into());
    }

    // dump filter
    trace!("seccomp: dump filter");
    let mut filter = vec![sock_filter {
        code: 0,
        jt: 0,
        jf: 0,
        k: 0,
    }; insn_count as usize];
    let ret = unsafe { ptrace(PTRACE_SECCOMP_GET_FILTER.try_into().unwrap(), pid, index, filter.as_mut_ptr() as *mut sock_filter) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // detach ptrace
    trace!("seccomp: detach ptrace");
    let ret = unsafe { ptrace(PTRACE_DETACH, pid, 0, 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // create sock_fprog
    let fprog = sock_fprog {
        len: insn_count as u16,
        filter: filter.as_mut_ptr(),
    };
    // set filter
    trace!("seccomp: set filter");
    let ret = unsafe { syscall(SYS_seccomp, SECCOMP_SET_MODE_FILTER, 0, &fprog as *const sock_fprog) };
    if ret < 0 {
        return Err(Errno::last().into());
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
    // usage: attach-ctr <init_pid> <workdir> <fd_fusebpf_mount_tree>
    let init_pid = std::env::args().nth(1).unwrap().parse::<i32>()?;
    let workdir = std::env::args().nth(2).unwrap();
    let new_rootfs_fd = unsafe { OwnedFd::from_raw_fd(std::env::args().nth(3).unwrap().parse::<i32>()?) };

    trace!("open pidfd");
    let pidfd = PidFd::open(init_pid)?;

    // easier to read everything here instead of keeping /proc/<pid> dirfd open for later
    trace!("read process info");
    let proc_status = std::fs::read_to_string(format!("/proc/{}/status", init_pid))?;
    let oom_score_adj = std::fs::read_to_string(format!("/proc/{}/oom_score_adj", init_pid))?;
    let proc_cgroup = std::fs::read_to_string(format!("/proc/{}/cgroup", init_pid))?;
    let proc_env = std::fs::read_to_string(format!("/proc/{}/environ", init_pid))?;
    let proc_mounts = std::fs::read_to_string(format!("/proc/{}/mounts", init_pid))?;
    let num_caps = std::fs::read_to_string("/proc/sys/kernel/cap_last_cap")?.trim_end().parse::<u32>()? + 1;

    // copy before entering container mount ns
    // /proc/self link reads ENOENT if pidns of mount and current process don't match
    trace!("copy oom score adj");
    std::fs::write("/proc/self/oom_score_adj", oom_score_adj)?;

    // cgroupfs in mount ns is mounted in container's cgroupns
    trace!("copy cgroup");
    let cg_path = proc_cgroup.lines().next().unwrap()
        .split(':')
        .last()
        .unwrap();
    let self_pid: i32 = getpid().into();
    std::fs::write(format!("/sys/fs/cgroup/{}/cgroup.procs", cg_path), format!("{}", self_pid))?;

    trace!("attach most namespaces");
    setns(&pidfd, CloneFlags::CLONE_NEWNS | CloneFlags::CLONE_NEWCGROUP | CloneFlags::CLONE_NEWUTS | CloneFlags::CLONE_NEWIPC | CloneFlags::CLONE_NEWNET | CloneFlags::CLONE_NEWPID)?;

    trace!("unshare mount ns");
    unshare(CloneFlags::CLONE_NEWNS)?;

    trace!("mounts: set propagation to private");
    mount_common("/", "/", None, MsFlags::MS_REC | MsFlags::MS_PRIVATE, None)?;

    // [mounts] mirror mounts and pseudo-filesystems onto /
    // to avoid weird fuse-bpf semantics, esp. for /proc
    // also helps preserve masked mounts (we don't use AT_RECURSIVE but we mirror all binds)
    // everything else (i.e. standard overlayfs) is OK going through fuse-bpf
    trace!("mounts: mirror mounts onto new rootfs");
    // save fuse-bpf path fds to keep inode/dentry alive
    let mut mounted_over_fds: Vec<OwnedFd> = Vec::new();
    for line in proc_mounts.lines() {
        // skip empty lines
        if line.is_empty() {
            continue;
        }

        // get mount path
        let path = line.split_whitespace().into_iter().nth(1).unwrap();
        // skip /
        if path == "/" {
            continue;
        }

        // open dest fd
        let dest_fd = unsafe { OwnedFd::from_raw_fd(openat(new_rootfs_fd.as_raw_fd(), path, OFlag::O_RDONLY | OFlag::O_CLOEXEC, Mode::empty())?) };
        // save to keep inode/dentry alive
        mounted_over_fds.push(dest_fd);

        let tree_fd = open_tree(path, OPEN_TREE_CLOEXEC | OPEN_TREE_CLONE)?;
        move_mount(&tree_fd, Some(&new_rootfs_fd), path)?;
    }

    // magic incantation from vinit
    trace!("mounts: mount fuse bpf onto /");
    fchdir(new_rootfs_fd.as_raw_fd())?;
    move_mount(&new_rootfs_fd, None, "/")?;
    chroot(".")?;

    trace!("set umask");
    umask(Mode::from_bits(0o022).unwrap());

    trace!("parse proc status info");
    let init_status = proc_status.lines()
        .map(|line| line.split_ascii_whitespace().collect::<Vec<&str>>())
        // parse into key-value <&str, Vec<String>>
        .map(|line| (line[0], line.iter().skip(1).map(|s| s.to_string()).collect::<Vec<String>>()))
        .collect::<std::collections::HashMap<&str, Vec<String>>>();

    trace!("copy gid");
    let gid = init_status.get("Gid:").unwrap().get(0).unwrap();
    setgid(gid.parse::<u32>()?.into())?;

    trace!("copy supplementary groups");
    let groups = init_status.get("Groups:").unwrap();
    setgroups(&groups.iter()
        .map(|s| s.parse::<u32>().unwrap().into())
        .collect::<Vec<Gid>>())?;

    trace!("copy NO_NEW_PRIVS");
    let no_new_privs = init_status.get("NoNewPrivs:").unwrap().get(0).unwrap();
    if no_new_privs == "1" {
        prctl::set_no_new_privs()?;
    }

    // copy env
    trace!("copy env");
    let mut cstr_envs = proc_env.split('\0')
        // append to PATH
        .map(|s| if s.starts_with("PATH=") { format!("PATH={}:{}", s, APPEND_PATH) } else { s.to_string() })
        .map(|s| CString::new(s))
        .collect::<anyhow::Result<Vec<_>, _>>()?;
    // append extra envs
    cstr_envs.reserve(EXTRA_ENV.len());
    for &env in EXTRA_ENV {
        cstr_envs.push(CString::new(env)?);
    }
    // inherit some envs from ssh client 
    cstr_envs.reserve(INHERIT_ENVS.len());
    for &env in INHERIT_ENVS {
        if let Ok(val) = std::env::var(env) {
            cstr_envs.push(CString::new(val)?);
        }
    }

    // close lingering fds before user-controlled chdir
    // keep mounted_over_fds in parent (waitpid) process to keep inode/dentry alive
    drop(new_rootfs_fd);

    // then chdir to requested workdir (must do / first to avoid rel path vuln)
    // can fail (falls back to /)
    _ = chdir(Path::new(&workdir));

    trace!("attach remaining namespaces");
    // entering current userns will return EINVAL. ignore that
    match setns(&pidfd, CloneFlags::CLONE_NEWUSER) {
        Ok(_) => {},
        Err(Errno::EINVAL) => trace!("set user ns failed with EINVAL, continuing"),
        Err(e) => return Err(e.into()),
    }

    trace!("copy rlimits");
    for &res in &[libc::RLIMIT_CPU, libc::RLIMIT_FSIZE, libc::RLIMIT_DATA, libc::RLIMIT_STACK, libc::RLIMIT_CORE, libc::RLIMIT_RSS, libc::RLIMIT_NPROC, libc::RLIMIT_NOFILE, libc::RLIMIT_MEMLOCK, libc::RLIMIT_AS, libc::RLIMIT_LOCKS, libc::RLIMIT_SIGPENDING, libc::RLIMIT_MSGQUEUE, libc::RLIMIT_NICE, libc::RLIMIT_RTPRIO, libc::RLIMIT_RTTIME] {
        let mut rlimit = libc::rlimit {
            rlim_cur: 0,
            rlim_max: 0,
        };
        // read init_pid's rlimit
        if unsafe { prlimit(init_pid, res, null(), &mut rlimit) } != 0 {
            return Err(Errno::last().into());
        }
        // write to self
        if unsafe { prlimit(0, res, &rlimit, null_mut()) } != 0 {
            return Err(Errno::last().into());
        }
    }

    // copy seccomp:
    // use ptrace + PTRACE_SECCOMP_GET_FILTER to dump BPF filters
    trace!("copy seccomp");
    let has_seccomp = init_status.get("Seccomp:").unwrap().get(0).unwrap() != "0";
    if has_seccomp {
        copy_seccomp_filter(init_pid, 0)?;
    }

    // copy capabilities and add CAP_SYS_PTRACE
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
    let ret = unsafe { libc::prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }
    for i in 0..num_caps {
        if cap_amb & (1 << i) != 0 {
            let ret = unsafe { libc::prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE, i as i32) };
            if ret < 0 {
                return Err(Errno::last().into());
            }
        }
    }
    // bounding: drop all unset caps
    trace!("copy capabilities: bounding");
    for i in 0..num_caps {
        if cap_bnd & (1 << i) == 0 {
            let ret = unsafe { libc::prctl(PR_CAPBSET_DROP, i as i32) };
            if ret < 0 {
                return Err(Errno::last().into());
            }
        }
    }
    // set eff/prm/inh
    trace!("copy capabilities: effective/permitted/inheritable");
    let cap_user_hdr = CapUserHeader {
        version: _LINUX_CAPABILITY_VERSION_3,
        pid: self_pid,
    };
    let cap_user_data = CapUserData {
        effective_lo: (cap_eff & 0xffffffff) as u32,
        permitted_lo: (cap_prm & 0xffffffff) as u32,
        inheritable_lo: (cap_inh & 0xffffffff) as u32,
        effective_hi: (cap_eff >> 32) as u32,
        permitted_hi: (cap_prm >> 32) as u32,
        inheritable_hi: (cap_inh >> 32) as u32,
    };
    let ret = unsafe { syscall(SYS_capset, &cap_user_hdr as *const CapUserHeader, &cap_user_data as *const CapUserData) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    trace!("fork into ns");
    match unsafe { fork()? } {
        ForkResult::Parent { child } => {
            // parent
            trace!("parent: waitpid");
            let res = unsafe { waitpid(child.as_raw(), null_mut(), 0) };
            if res < 0 {
                return Err(Errno::last().into());
            }
        }
        ForkResult::Child => {
            // child
            // TODO: must double fork and exit intermediate child to reparent to pid 1
            trace!("child: execve");
            execve(&CString::new("/bin/sh")?, &[CString::new("-sh")?], &cstr_envs)?;
        }
    }

    Ok(())
}
