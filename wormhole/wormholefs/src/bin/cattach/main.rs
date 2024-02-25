use std::{error::Error, ffi::CString, os::fd::{AsRawFd, FromRawFd, OwnedFd}, ptr::{null, null_mut}};

use libc::{prlimit, ptrace, sock_filter, sock_fprog, syscall, waitpid, SYS_capset, SYS_move_mount, SYS_open_tree, SYS_seccomp, AT_FDCWD, AT_RECURSIVE, MOVE_MOUNT_F_EMPTY_PATH, OPEN_TREE_CLOEXEC, OPEN_TREE_CLONE, PR_CAPBSET_DROP, PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, PR_CAP_AMBIENT_RAISE, PTRACE_DETACH, PTRACE_SEIZE, SECCOMP_SET_MODE_FILTER};
use nix::{errno::Errno, mount::MsFlags, sched::{setns, unshare, CloneFlags}, sys::{prctl, stat::{umask, Mode}}, unistd::{chdir, chroot, execve, fork, getpid, setgid}};
use pidfd::PidFd;

mod pidfd;

const PTRACE_SECCOMP_GET_FILTER: u32 = 0x420c;

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

fn mount_common(source: &str, dest: &str, fstype: Option<&str>, flags: MsFlags, data: Option<&str>) -> Result<(), Box<dyn Error>> {
    nix::mount::mount(Some(source), dest, fstype, flags, data)?;
    Ok(())
}

fn open_tree(path: &str) -> Result<OwnedFd, Box<dyn Error>> {
    let path = CString::new(path)?;
    let fd = unsafe { syscall(SYS_open_tree, AT_FDCWD, path.into_raw(), OPEN_TREE_CLOEXEC | OPEN_TREE_CLONE | (AT_RECURSIVE as u32)) };
    if fd < 0 {
        return Err(Errno::last().into());
    }
    Ok(unsafe { OwnedFd::from_raw_fd(fd as i32) })
}

fn move_mount(tree_fd: &OwnedFd, dest: &str) -> Result<(), Box<dyn Error>> {
    let dest = CString::new(dest)?;
    let empty_cstring = CString::new("")?;
    let res = unsafe { syscall(SYS_move_mount, tree_fd.as_raw_fd(), empty_cstring.into_raw(), AT_FDCWD, dest.into_raw(), MOVE_MOUNT_F_EMPTY_PATH) };
    if res < 0 {
        return Err(Errno::last().into());
    }
    Ok(())
}

fn copy_seccomp_filter(pid: i32, index: u32) -> Result<(), Box<dyn Error>> {
    // attach via ptrace
    // PTRACE_SEIZE avoids stopping the tracee
    let ret = unsafe { ptrace(PTRACE_SEIZE, pid, 0, 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // wait for ptrace to attach
    let ret = unsafe { waitpid(pid, null_mut(), 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // get instruction count in seccomp filter
    let insn_count = unsafe { ptrace(PTRACE_SECCOMP_GET_FILTER, pid, index, null::<sock_filter>()) };
    if insn_count < 0 {
        return Err(Errno::last().into());
    }

    // dump filter
    let mut filter = vec![sock_filter {
        code: 0,
        jt: 0,
        jf: 0,
        k: 0,
    }; insn_count as usize];
    let ret = unsafe { ptrace(PTRACE_SECCOMP_GET_FILTER, pid, index, filter.as_mut_ptr() as *mut sock_filter) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    // detach ptrace
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
    let ret = unsafe { syscall(SYS_seccomp, SECCOMP_SET_MODE_FILTER, 0, &fprog as *const sock_fprog) };
    if ret < 0 {
        return Err(Errno::last().into());
    }

    Ok(())
}

fn main() -> Result<(), Box<dyn Error>> {
    // stdin, stdout, stderr are expected to be 0,1,2 and will be propagated to the child
    // usage: attach-ctr <init_pid> <fd_fusebpf_mount_tree>
    let init_pid = std::env::args().nth(1).unwrap().parse::<i32>()?;
    let fusebpf_mount_tree_fd = unsafe { OwnedFd::from_raw_fd(std::env::args().nth(2).unwrap().parse::<i32>()?) };
    
    // open pidfd
    let pidfd = PidFd::open(init_pid)?;

    // read process info
    let proc_status = std::fs::read_to_string(format!("/proc/{}/status", init_pid))?;
    let oom_score_adj = std::fs::read_to_string(format!("/proc/{}/oom_score_adj", init_pid))?;
    let proc_cgroup = std::fs::read_to_string(format!("/proc/{}/cgroup", init_pid))?;
    let proc_env = std::fs::read_to_string(format!("/proc/{}/environ", init_pid))?;

    // attach mount ns
    setns(&pidfd, CloneFlags::CLONE_NEWNS)?;

    // unshare mount ns
    unshare(CloneFlags::CLONE_NEWNS)?;

    // [mounts] set propagation to private
    mount_common("/", "/", None, MsFlags::MS_REC | MsFlags::MS_PRIVATE, None).unwrap();

    // grab tree fds for pseudo-filesystems
    let proc_tree_fd = open_tree("/proc")?;
    let sys_tree_fd = open_tree("/sys")?;
    let dev_tree_fd = open_tree("/dev")?;

    // [mounts] mount fuse bpf onto /
    move_mount(&fusebpf_mount_tree_fd, "/")?;

    // [mounts] mirror standard pseudo-filesystems onto / (to avoid weird fuse-bpf semantics, esp. for /proc)
    // also helps preserve masked mounts (we call open_tree with AT_RECURSIVE)
    // other mounts are OK going through fuse-bpf
    move_mount(&proc_tree_fd, "/proc")?;
    move_mount(&sys_tree_fd, "/sys")?;
    move_mount(&dev_tree_fd, "/dev")?;

    // set standard umask
    umask(Mode::from_bits(0o022).unwrap());

    // parse /proc/<initpid>/status into map
    let init_status = proc_status.lines()
        .map(|line| line.split_ascii_whitespace().collect::<Vec<&str>>())
        // parse into key-value <&str, Vec<String>>
        .map(|line| (line[0], line.iter().skip(1).map(|s| s.to_string()).collect::<Vec<String>>()))
        .collect::<std::collections::HashMap<&str, Vec<String>>>();

    // copy gid
    let gid = init_status.get("Gid:").unwrap().get(0).unwrap();
    setgid(gid.parse::<u32>()?.into())?;

    // copy supplementary groups
    let groups = init_status.get("Groups:").unwrap();
    for group in groups.iter() {
        let group = group.parse::<u32>()?;
        if group != 0 {
            setgid(group.into())?;
        }
    }

    // copy oom score adj from /proc/<initpid>/oom_score_adj;
    std::fs::write("/proc/self/oom_score_adj", oom_score_adj)?;

    // copy rlimits from prlimit
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

    // copy cgroup
    let cg_path = proc_cgroup.lines().next().unwrap()
        .split(':')
        .last()
        .unwrap();
    let self_pid: i32 = getpid().into();
    std::fs::write(format!("/sys/fs/cgroup/{}/cgroup.procs", cg_path), format!("{}", self_pid))?;

    // copy NO_NEW_PRIVS
    // TODO: test this
    let no_new_privs = init_status.get("NoNewPrivs:").unwrap().get(0).unwrap();
    if no_new_privs == "1" {
        prctl::set_no_new_privs()?;
    }

    // copy seccomp:
    // use ptrace + PTRACE_SECCOMP_GET_FILTER to dump BPF filters
    let has_seccomp = init_status.get("Seccomp:").unwrap().get(0).unwrap() != "0";
    if has_seccomp {
        copy_seccomp_filter(init_pid, 0)?;
    }

    // copy capabilities and add CAP_SYS_PTRACE
    // must be after seccomp: if we drop CAP_SYS_ADMIN and don't have NO_NEW_PRIVS, we can't set a seccomp filter
    // works because docker's seccomp filter allows capset/capget
    // order: ambient, bounding, effective, inheritable, permitted
    let cap_inh = u64::from_str_radix(init_status.get("CapInh:").unwrap().get(0).unwrap(), 16)?;
    let cap_prm = u64::from_str_radix(init_status.get("CapPrm:").unwrap().get(0).unwrap(), 16)?;
    let cap_eff = u64::from_str_radix(init_status.get("CapEff:").unwrap().get(0).unwrap(), 16)?;
    let cap_bnd = u64::from_str_radix(init_status.get("CapBnd:").unwrap().get(0).unwrap(), 16)?;
    let cap_amb = u64::from_str_radix(init_status.get("CapAmb:").unwrap().get(0).unwrap(), 16)?;
    // ambient: clear all, then raise set caps
    let ret = unsafe { libc::prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_CLEAR_ALL, 0) };
    if ret < 0 {
        return Err(Errno::last().into());
    }
    for i in 0..64 {
        if cap_amb & (1 << i) != 0 {
            let ret = unsafe { libc::prctl(PR_CAP_AMBIENT, PR_CAP_AMBIENT_RAISE, i as i32) };
            if ret < 0 {
                return Err(Errno::last().into());
            }
        }
    }
    // bounding: drop all unset caps
    for i in 0..64 {
        if cap_bnd & (1 << i) == 0 {
            let ret = unsafe { libc::prctl(PR_CAPBSET_DROP, i as i32) };
            if ret < 0 {
                return Err(Errno::last().into());
            }
        }
    }
    // set eff/prm/inh
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

    // copy env
    let cstr_envs = proc_env.split('\0')
        .map(|s| CString::new(s))
        .collect::<Result<Vec<_>, _>>()?;

    // close lingering fds:
    // pidfd is OK: it's a pid inside the pidns, so doesn't need to be closed
    // this is also OK but close to prevent leak
    drop(fusebpf_mount_tree_fd);

    // chroot and chdir to avoid leaking mounts from host
    chroot("/")?;
    chdir("/")?;

    // attach remaining namespaces
    setns(&pidfd, CloneFlags::CLONE_NEWCGROUP | CloneFlags::CLONE_NEWUTS | CloneFlags::CLONE_NEWIPC | CloneFlags::CLONE_NEWNET | CloneFlags::CLONE_NEWUSER | CloneFlags::CLONE_NEWPID)?;

    // fork to reparent to pid 1 in ns
    let res = unsafe { fork()? };
    if res.is_child() {
        // child
        execve(&CString::new("/nix/bin/zsh")?, &[CString::new("-zsh")?], &cstr_envs)?;
    }

    Ok(())
}
