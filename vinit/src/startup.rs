use std::{env, error::Error, fs::{self, Permissions, OpenOptions}, time::{Instant, Duration}, os::{unix::{prelude::PermissionsExt, fs::chroot}}, process::{Command, Stdio}, io::{Write}, net::UdpSocket, sync::Arc};

use mkswap::SwapWriter;
use netlink_packet_route::{LinkMessage, link};
use nix::{sys::{stat::{umask, Mode}, resource::{setrlimit, Resource}, time::TimeSpec, mman::{mlockall, MlockAllFlags}}, mount::{MsFlags}, unistd::{sethostname}, libc::{RLIM_INFINITY, self}, time::{clock_settime, ClockId, clock_gettime}};
use futures_util::TryStreamExt;
use tracing::log::debug;

use crate::{helpers::{sysctl, SWAP_FLAG_DISCARD, SWAP_FLAG_PREFER, SWAP_FLAG_PRIO_SHIFT, SWAP_FLAG_PRIO_MASK}, DEBUG, blockdev, SystemInfo, ethtool, InitError, Timeline, vcontrol, action::SystemAction};
use crate::service::{ServiceTracker, Service};
use tokio::{sync::{Mutex, mpsc::{Sender}}};

use crate::ethtool::ETHTOOL_STSO;

// da:9b:d0:64:e1:01
const VNET_LLADDR: &[u8] = &[0xda, 0x9b, 0xd0, 0x64, 0xe1, 0x01];
const VNET_NEIGHBORS: &[&str] = &[
    "198.19.248.1",
    "198.19.248.200",
    "198.19.248.201",
    "198.19.248.253",
    "198.19.248.254",
    // only one IPv6: others are on ext subnet (to avoid NDP)
    "fd07:b51a:cc66:00f0::1",
];

const FS_CORRUPTED_MSG: &str = r#"
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!! DATA IS LIKELY CORRUPTED.
!! Please make a backup, consider reporting this issue at https://orbstack.dev/issues/bug, and delete OrbStack data to continue.
!!
!! Giving up and shutting down now.
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
"#;

fn set_basic_env() -> Result<(), Box<dyn Error>> {
    // umask: self write, others read
    umask(Mode::from_bits_truncate(0o022));

    // environment variables
    env::set_var("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin");
    // /etc/profile.d/locale.sh
    env::set_var("CHARSET", "UTF-8");
    env::set_var("LANG", "C");
    env::set_var("LC_COLLATE", "C");

    // hostname
    sethostname("orbhost")?;

    // ulimit
    setrlimit(Resource::RLIMIT_NOFILE, 1048576, 1048576)?;
    setrlimit(Resource::RLIMIT_MEMLOCK, RLIM_INFINITY, RLIM_INFINITY)?;

    Ok(())
}

fn mount_common(source: &str, dest: &str, fstype: Option<&str>, flags: MsFlags, data: Option<&str>) -> Result<(), Box<dyn Error>> {
    if let Err(e) = nix::mount::mount(Some(source), dest, fstype, flags, data) {
        return Err(InitError::Mount {
            source: source.to_string(),
            dest: dest.to_string(),
            error: e,
        }.into());
    }

    Ok(())
}

fn mount(source: &str, dest: &str, fstype: &str, flags: MsFlags, data: Option<&str>) -> Result<(), Box<dyn Error>> {
    mount_common(source, dest, Some(fstype), flags, data)
}

fn bind_mount(source: &str, dest: &str, flags: Option<MsFlags>) -> Result<(), Box<dyn Error>> {
    mount_common(source, dest, None, flags.unwrap_or(MsFlags::empty()) | MsFlags::MS_BIND, None)
}

fn setup_overlayfs() -> Result<(), Box<dyn Error>> {
    let merged_flags = MsFlags::MS_NOATIME;
    // secure flags for overlay
    // this is used for Docker rootfs so don't pass "noexec". or people can't install packages in Docker machine
    // also don't pass nodev or overlayfs whiteouts won't work
    let upper_flags = merged_flags | MsFlags::MS_NOSUID;
    mount("tmpfs", "/run", "tmpfs", upper_flags, None)?;
    // create directories
    fs::create_dir_all("/run/overlay/root")?;
    fs::create_dir_all("/run/overlay/upper")?;
    fs::create_dir_all("/run/overlay/work")?;
    fs::create_dir_all("/run/overlay/merged")?;
    // bind mount root
    bind_mount("/", "/run/overlay/root", None)?;
    // mount overlayfs - with vanity name for "df"
    mount("orbstack", "/run/overlay/merged", "overlay", merged_flags, Some("lowerdir=/run/overlay/root,upperdir=/run/overlay/upper,workdir=/run/overlay/work"))?;

    // make original fs available for debugging
    if DEBUG {
        fs::create_dir_all("/run/overlay/merged/orig/run")?;
        bind_mount("/run", "/run/overlay/merged/orig/run", None)?;
        fs::create_dir_all("/run/overlay/merged/orig/root")?;
        bind_mount("/", "/run/overlay/merged/orig/root", None)?;
    }

    // switch root
    /*
    equivalent to:
        cd /run/overlay/merged
        mount --move . /
        chroot .
    */
    env::set_current_dir("/run/overlay/merged")?;
    mount_common(".", "/", None, MsFlags::MS_MOVE, None)?;
    chroot(".")?;

    Ok(())
}

fn mount_pseudo_fs() -> Result<(), Box<dyn Error>> {
    let secure_flags = MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID | MsFlags::MS_NODEV | MsFlags::MS_RELATIME;
    let dev_flags = MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID | MsFlags::MS_RELATIME;

    // essential
    mount("sysfs", "/sys", "sysfs", secure_flags, None)?;
    apply_perf_tuning_early()?;
    mount("proc", "/proc", "proc", secure_flags, None)?;
    mount("devtmpfs", "/dev", "devtmpfs", dev_flags, Some("mode=0755"))?;
    // extra
    fs::create_dir_all("/dev/pts")?;
    mount("devpts", "/dev/pts", "devpts", dev_flags, Some("mode=0620,gid=5,ptmxmode=000"))?;
    mount("securityfs", "/sys/kernel/security", "securityfs", secure_flags, None)?;
    mount("debugfs", "/sys/kernel/debug", "debugfs", secure_flags, None)?;
    fs::create_dir_all("/dev/mqueue")?;
    mount("mqueue", "/dev/mqueue", "mqueue", secure_flags, None)?;
    mount("fusectl", "/sys/fs/fuse/connections", "fusectl", secure_flags, None)?;
    mount("binfmt_misc", "/proc/sys/fs/binfmt_misc", "binfmt_misc", secure_flags, None)?;
    mount("tracefs", "/sys/kernel/tracing", "tracefs", secure_flags, None)?;
    mount("bpf", "/sys/fs/bpf", "bpf", secure_flags, Some("mode=0700"))?;
    // tmp
    fs::create_dir_all("/dev/shm")?;
    mount("shm", "/dev/shm", "tmpfs", secure_flags, Some("mode=1777"))?;
    mount("tmpfs", "/run", "tmpfs", secure_flags, Some("mode=0755"))?;
    mount("tmpfs", "/tmp", "tmpfs", secure_flags, Some("mode=0755"))?;

    // cgroup2 (nsdelegate for delegation/confinement)
    mount("cgroup", "/sys/fs/cgroup", "cgroup2", secure_flags, Some("nsdelegate"))?;

    // nfsd
    mount("nfsd", "/proc/fs/nfsd", "nfsd", secure_flags, None)?;

    Ok(())
}

fn apply_perf_tuning_early() -> Result<(), Box<dyn Error>> {
    // expedited RCU
    // speeds up container startup ~2x:
    // machine startup 4x: 260 -> 40 ms
    // low cost in practice (no IPI for idle CPUs): https://docs.kernel.org/RCU/Design/Expedited-Grace-Periods/Expedited-Grace-Periods.html
    // do it here instead of kernel to make it less obvious. as early as possible in userspace
    fs::write("/sys/kernel/rcu_expedited", "1")?;
    Ok(())
}

fn apply_perf_tuning_late() -> Result<(), Box<dyn Error>> {
    // reduce idle cpu usage
    sysctl("vm.compaction_proactiveness", "0")?;
    sysctl("vm.stat_interval", "30")?;

    // res limits
    sysctl("kernel.pid_max", "4194304")?;
    // match systemd
    // https://github.com/systemd/systemd/commit/a8b627aaed409a15260c25988970c795bf963812#diff-03b3e8b6554bb8ccd539ad2e547d9ef13f80428101bdc01b4d6e9ea5f685fe7c
    sysctl("fs.file-max", "9223372036854775807")?;
    sysctl("fs.aio-max-nr", "1048576")?;
    sysctl("fs.nr_open", "1073741816")?;

    // lxd recommended
    sysctl("fs.inotify.max_queued_events", "1048576")?;
    sysctl("fs.inotify.max_user_instances", "1048576")?;
    sysctl("fs.inotify.max_user_watches", "1048576")?;
    // no point for this use case
    //sysctl("kernel.dmesg_restrict", "1")?;
    sysctl("kernel.keys.maxbytes", "2000000")?;
    sysctl("kernel.keys.maxkeys", "2000")?;
    sysctl("net.ipv4.neigh.default.gc_thresh3", "8192")?;
    sysctl("net.ipv6.neigh.default.gc_thresh3", "8192")?;
    sysctl("vm.max_map_count", "262144")?;

    // lxd net tuning (= ~min tcp_mem)
    sysctl("net.core.netdev_max_backlog", "16384")?;

    // k8s
    sysctl("vm.panic_on_oom", "0")?;
    sysctl("kernel.panic_on_oops", "1")?;
    // fake this one
    //sysctl("kernel.panic", "10")?;
    sysctl("kernel.keys.root_maxkeys", "1000000")?;
    sysctl("kernel.keys.root_maxbytes", "25000000")?;

    // redis https://docs.bitnami.com/kubernetes/infrastructure/redis-cluster/administration/configure-kernel-settings/
    sysctl("net.core.somaxconn", "10000")?;

    // unpriv ping
    sysctl("net.ipv4.ping_group_range", "0 2147483647")?;

    // security
    sysctl("fs.protected_hardlinks", "1")?;
    sysctl("fs.protected_symlinks", "1")?;

    // block - disk performance tuning
    // this is slow (80 ms per disk), so do it in parallel
    let mut handles = vec![];
    for disk in ["vda", "vdb", "vdc"].iter() {
        let disk = disk.to_string();
        handles.push(std::thread::spawn(move || {
            fs::write(format!("/sys/block/{}/queue/scheduler", disk), "none").unwrap();
        }));
    }
    for handle in handles {
        handle.join().unwrap();
    }

    Ok(())
}

fn maybe_disable_tso(name: &str, link: &LinkMessage) -> Result<(), Box<dyn Error>> {
    // disable TSO if mtu == 1500
    // this is for vmnet bridge interfaces:
    /*
    eth0 = gvisor (which doesn't care about MTU and packet size)
        * macOS 12 doesn't let us set MTU on virtio-net
        * it rejects big packets from host
        * but allows guest to send big packets out
        * so we abuse this for fast asymmetrical network
    eth1, eth2, ... = vmnet bridge interfaces or vlan router
        * vmnet only supports symmetrical MTU and rejects packets bigger than
          MTU with packetTooBig, so we can't stuff 65K packets through from
          guest->host and limit to 1500 from host->guest
    */
    if let Some(mtu) = link.nlas.iter().find_map(|nla| {
        if let link::nlas::Nla::Mtu(mtu) = nla {
            Some(*mtu)
        } else {
            None
        }
    }) {
        if mtu == 1500 {
            //println!("  - Disabling TSO on {}", name);
            ethtool::set(name, ETHTOOL_STSO, 0)?;
        }
    }

    Ok(())
}

async fn setup_network() -> Result<(), Box<dyn Error>> {
    // don't send IPv6 router solicitations
    sysctl("net.ipv6.conf.all.accept_ra", "0")?;
    sysctl("net.ipv6.conf.default.accept_ra", "0")?;
    // and fix tentative IPv6 delay
    sysctl("net.ipv6.conf.eth0.accept_dad", "0")?;

    // scon net
    sysctl("net.ipv4.ip_forward", "1")?;
    sysctl("net.ipv6.conf.all.forwarding", "1")?;

    // connect to rtnetlink
    let (conn, handle, _) = rtnetlink::new_connection()?;
    let conn_task = tokio::spawn(conn);
    let mut ip_link = handle.link();
    let ip_addr = handle.address();
    let ip_route = handle.route();
    let ip_neigh = handle.neighbours();

    // loopback: set lo up
    let lo = ip_link.get().match_name("lo".into()).execute().try_next().await?.unwrap();
    ip_link.set(lo.header.index).up().execute().await?;

    // main gvisor NAT network
    let eth0 = ip_link.get().match_name("eth0".into()).execute().try_next().await?.unwrap();

    // static neighbors to reduce ARP CPU usage
    for ip_addr in VNET_NEIGHBORS {
        ip_neigh.add(eth0.header.index, ip_addr.parse()?)
            .link_local_address(VNET_LLADDR)
            .execute().await?;
    }

    // set eth0 mtu, up
    ip_link.set(eth0.header.index)
        .mtu(1500)
        .up()
        .execute().await?;

    // add IP addresses
    ip_addr.add(eth0.header.index, "198.19.248.2".parse()?, 24).execute().await?;
    ip_addr.add(eth0.header.index, "fd07:b51a:cc66:00f0::2".parse()?, 64).execute().await?;

    // add default routes
    ip_route.add().v4().gateway("198.19.248.1".parse()?).execute().await?;
    ip_route.add().v6().gateway("fd07:b51a:cc66:00f0::1".parse()?).execute().await?;

    // scon machine bridge: eth1 mtu, up
    // scon deals with the rest
    // cannot use static neigh because macOS generates MAC addr
    let eth1 = ip_link.get().match_name("eth1".into()).execute().try_next().await?.unwrap();
    maybe_disable_tso("eth1", &eth1)?;
    ip_link.set(eth1.header.index)
        .mtu(1500)
        .up()
        .execute().await?;

    // docker vlan router
    // scon deals with the rest
    let eth2 = ip_link.get().match_name("eth2".into()).execute().try_next().await?.unwrap();
    maybe_disable_tso("eth2", &eth2)?;
    ip_link.set(eth2.header.index)
        .mtu(1500)
        .up()
        .execute().await?;

    conn_task.abort();
    Ok(())
}

pub fn sync_clock(allow_backward: bool) -> Result<(), Box<dyn Error>> {
    // sync clock immediately at boot (if RTC is wrong) or on wake (until chrony kicks in)
    let socket = UdpSocket::bind("0.0.0.0:0")?;
    socket.set_read_timeout(Some(Duration::from_secs(10)))?;
    let host_time = sntpc::simple_get_time("198.19.248.200:123", socket)
        .map_err(|e| InitError::NtpGetTime(e))?;
    
    let sec = host_time.sec() as i64;
    let nsec = sntpc::fraction_to_nanoseconds(host_time.sec_fraction()) as i64;

    let new_time = TimeSpec::new(sec, nsec);
    let current_time = clock_gettime(ClockId::CLOCK_REALTIME)?;
    // never go back in time after boot
    if !allow_backward && new_time < current_time {
        debug!("Skipping clock step: would go back in time");
        return Ok(());
    }

    clock_settime(ClockId::CLOCK_REALTIME, new_time)?;

    println!("  - System time updated");
    Ok(())
}

fn resize_data(sys_info: &SystemInfo) -> Result<(), Box<dyn Error>> {
    // resize data partition
    // scon resizes the filesystem
    if let Some(value) = sys_info.seed_configs.get("data_size") {
        let new_size_mib = value.parse::<u64>()?;
        // get existing size
        let old_size_mib = blockdev::getsize64("/dev/vdb1")? / 1024 / 1024;
        // for safety, only allow increasing size
        if new_size_mib > old_size_mib {
            // resize
            println!("  - Resizing data to {} MiB", new_size_mib);
            let script = format!(",{}M\n", new_size_mib);
            let mut process = Command::new("sfdisk")
                .arg("--force")
                .arg("/dev/vdb")
                .stdin(Stdio::piped())
                .spawn()?;
            process.stdin.take().unwrap().write_all(script.as_bytes())?;
            let status = process.wait()?;
            if !status.success() {
                return Err(InitError::ResizeDataFs(status).into());
            }
        } else if new_size_mib < old_size_mib {
            eprintln!("WARNING: Attempted to shrink data partition from {} MiB to {} MiB", old_size_mib, new_size_mib);
        }
    }

    Ok(())
}

fn mount_data() -> Result<(), Box<dyn Error>> {
    // virtiofs share
    mount("mac", "/mnt/mac", "virtiofs", MsFlags::MS_RELATIME, None)?;

    // data
    // first try with regular mount, then try usebackuproot
    let data_flags = MsFlags::MS_NOATIME;
    let fs_options = "discard,space_cache=v2,ssd,nodatacow,nodatasum,quota_statfs";
    if let Err(e) = mount("/dev/vdb1", "/data", "btrfs", data_flags, Some(fs_options)) {
        eprintln!(" !!! Failed to mount data: {}", e);
        println!(" [*] Attempting to recover data");
        if let Err(e) = mount("/dev/vdb1", "/data", "btrfs", data_flags, Some(format!("{},rescue=usebackuproot", fs_options).as_str())) {
            eprintln!(" !!! Failed to recover data: {}", e);
            eprintln!("{}", FS_CORRUPTED_MSG);
            return Err(e);
        }
    }

    Ok(())
}

fn init_data() -> Result<(), Box<dyn Error>> {
    // guest tools
    fs::create_dir_all("/data/guest-state/bin/cmdlinks")?;
    fs::set_permissions("/data/guest-state/bin", Permissions::from_mode(0o777))?;
    bind_mount("/data/guest-state", "/opt/orbstack-guest/data", None)?;

    // debug root home
    if DEBUG {
        fs::create_dir_all("/data/dev-root-home/.ssh")?;
        fs::copy("/root/.ssh/authorized_keys", "/data/dev-root-home/.ssh/authorized_keys")?;
        fs::set_permissions("/data/dev-root-home/.ssh", Permissions::from_mode(0o700))?;
        fs::set_permissions("/data/dev-root-home/.ssh/authorized_keys", Permissions::from_mode(0o600))?;
        bind_mount("/data/dev-root-home", "/root", None)?;
    }

    // set up NFS root for scon
    // mount name is visible in machines bind mount, so use vanity name
    mount("machines", "/nfsroot-rw", "tmpfs", MsFlags::MS_NOATIME, Some("mode=0755"))?;
    fs::copy("/opt/orb/nfs-readme.txt", "/nfsroot-rw/README.txt")?;
    // attempt to reduce NFS CPU usage from macOS indexing
    fs::write("/nfsroot-rw/.metadata_never_index", "")?;
    fs::write("/nfsroot-rw/.metadata-never-index", "")?;
    fs::write("/nfsroot-rw/.metadata_never_index_unless_rootfs", "")?;
    fs::create_dir_all("/nfsroot-rw/.fseventsd")?;
    fs::write("/nfsroot-rw/.fseventsd/no_log", "")?;
    // read-only bind (+ rshared, for scon bind mounts)
    bind_mount("/nfsroot-rw", "/nfsroot-ro", Some(MsFlags::MS_RDONLY | MsFlags::MS_REC | MsFlags::MS_SHARED))?;

    Ok(())
}

fn add_binfmt(name: &str, magic: &str, mask: Option<&str>, interpreter: &str, flags: &str) -> Result<(), Box<dyn Error>> {
    let offset = 0;
    let buf = format!(":{}:M:{}:{}:{}:{}:{}", name, offset, magic, mask.unwrap_or(""), interpreter, flags);
    fs::write("/proc/sys/fs/binfmt_misc/register", buf)?;
    Ok(())
}

#[cfg(target_arch = "x86_64")]
fn setup_emulators(sys_info: &SystemInfo) -> Result<(), Box<dyn Error>> {
    // arm64 qemu
    add_binfmt("qemu-aarch64", r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xb7\x00"#, Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[qemu-arm64]", "POCF")?;
    Ok(())
}

#[cfg(target_arch = "aarch64")]
fn setup_emulators(sys_info: &SystemInfo) -> Result<(), Box<dyn Error>> {
    // we always register qemu, but flags change if using Rosetta
    let mut qemu_flags = "POCF".to_string();

    if let Ok(_) = mount("rosetta", "/mnt/rosetta", "virtiofs", MsFlags::empty(), None) {
        // rosetta
        println!("  -  Using Rosetta");

        let mut rosetta_flags = "CF@(".to_string();
        // add preserve-argv0 flag on Sonoma
        if let Some(value) = sys_info.seed_configs.get("host_major_version") {
            let version = value.parse::<u32>()?;
            if version >= 14 {
                rosetta_flags += "P";
            }
        }

        // if we're using Rosetta, we'll do it through the RVFS wrapper.
        // add flag to register qemu-x86_64 as a hidden handler that the RVFS wrapper can use, via comm=rvk2
        qemu_flags += ")"; // MISC_FMT_ORBRVK2

        // register RVFS wrapper first
        // entries added later take priority, so MUST register first to avoid infinite loop
        // WARNING: NOT THREAD SAFE! this uses chdir.
        //          luckily init doesn't care about cwd during early boot (but later, it matters for spawned processes)
        env::set_current_dir("/opt/orb/extra-links")?;
        let rvfs_res = add_binfmt("rosetta", r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00"#, Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[rosetta]", "POCF");
        env::set_current_dir("/")?;
        rvfs_res?;

        // then register real rosetta with comm=rvk1 key '('
        add_binfmt("rosetta1", r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00"#, Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[rosetta]", &rosetta_flags)?;
    } else {
        // qemu
        println!("  -  Using QEMU");
    }

    // always register qemu x86_64
    // if Rosetta mode: RVFS wrapper may choose to invoke it via task comm=rvk2 key (we add ')' flag)
    // if QEMU mode: it will always be used
    add_binfmt("qemu-x86_64", r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00"#, Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[qemu]", &qemu_flags)?;

    // always use qemu for i386 (32-bit)
    // Rosetta doesn't support 32-bit
    add_binfmt("qemu-i386", r#"\x7f\x45\x4c\x46\x01\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x03\x00"#, Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\xfc\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[qemu32]", "POCF")?;

    Ok(())
}

fn setup_binfmt(sys_info: &SystemInfo) -> Result<(), Box<dyn Error>> {
    setup_emulators(sys_info)?;

    // Mach-O
    // no O because fds can't cross OS boundary
    // no C because credentials are ignored
    // macOS doesn't support 32-bit anymore
    // 07 = x86
    add_binfmt("mac-macho-x86_64", r#"\xcf\xfa\xed\xfe\x07\x00\x00\x01"#, None, "[mac]", "FP")?;
    // only for arm64
    #[cfg(target_arch = "aarch64")]
    add_binfmt("mac-macho-arm64", r#"\xcf\xfa\xed\xfe\x0c\x00\x00\x01"#, None, "[mac]", "FP")?;

    // macOS Universal (either arch first)
    // accepts both 1 and 2 binaries, with either arch first
    // no conflict with java: https://github.com/file/file/blob/c8bba134ac1f3c9f5b052486a7694c5b48e498bc/magic/Magdir/cafebabe#L3
    add_binfmt("mac-universal-x86_64", r#"\xca\xfe\xba\xbe\x00\x00\x00\x02\x01\x00\x00\x07"#, Some(r#"\xff\xff\xff\xff\xff\xff\xff\x02\xff\xff\xff\xff"#), "[mac]", "FP")?;
    add_binfmt("mac-universal-arm64", r#"\xca\xfe\xba\xbe\x00\x00\x00\x02\x01\x00\x00\x0c"#, Some(r#"\xff\xff\xff\xff\xff\xff\xff\x02\xff\xff\xff\xff"#), "[mac]", "FP")?;

    Ok(())
}

fn enable_swap(path: &str, priority: i32) -> Result<(), Box<dyn Error>> {
    unsafe {
        let path = std::ffi::CString::new(path)?;
        // allow discard to free zram pages
        let res = libc::swapon(path.as_ptr(), SWAP_FLAG_DISCARD | SWAP_FLAG_PREFER | (priority << SWAP_FLAG_PRIO_SHIFT) & SWAP_FLAG_PRIO_MASK);
        if res != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }

    Ok(())
}

fn setup_memory() -> Result<(), Box<dyn Error>> {
    // prevent us from getting swapped out in case of memory pressure
    // (~8 ms, so it's in this async task)
    mlockall(MlockAllFlags::MCL_CURRENT | MlockAllFlags::MCL_FUTURE)?;

    // sysctls
    sysctl("vm.overcommit_memory", "1")?;
    sysctl("vm.swappiness", "20")?;
    sysctl("vm.page-cluster", "1")?;
    sysctl("vm.watermark_boost_factor", "0")?;

    // MGLRU thrashing protection
    fs::write("/sys/kernel/mm/lru_gen/min_ttl_ms", "1000")?;

    // zram
    // size = 1x RAM
    let mem_total_kib = fs::read_to_string("/proc/meminfo")?
        .lines()
        .find(|l| l.starts_with("MemTotal:"))
        .unwrap()
        .split_whitespace()
        .nth(1)
        .unwrap()
        .parse::<u64>()?;
    fs::write("/sys/block/zram0/backing_dev", "/dev/vdc1")?;
    fs::write("/sys/block/zram0/disksize", format!("{}", mem_total_kib * 1024))?;
    fs::write("/sys/block/zram0/writeback", "huge_idle")?;
    // create swap
    let zram_dev = OpenOptions::new()
        .read(true)
        .write(true)
        .open("/dev/zram0")?;
    SwapWriter::new()
        .write(zram_dev)?;
    // enable
    enable_swap("/dev/zram0", 32767)?;

    // emergency disk swap (2 GiB)
    enable_swap("/dev/vdc2", 1)?;

    Ok(())
}

async fn start_services(service_tracker: Arc<Mutex<ServiceTracker>>, sys_info: &SystemInfo) -> Result<(), Box<dyn Error>> {
    let mut service_tracker = service_tracker.lock().await;

    // chrony
    service_tracker.spawn(Service::CHRONY, &mut Command::new("/usr/sbin/chronyd")
        .arg(if DEBUG { "-d" } else { "-n" }) // foreground (-d for log-to-stderr)
        .arg("-f") // config file
        .arg("/etc/chrony/chrony.conf"))?;

    // udev
    // this is only for USB devices, nbd, etc. so no need to wait for it to settle
    service_tracker.spawn(Service::UDEV, &mut Command::new("/sbin/udevd"))?;

    // scon
    service_tracker.spawn(Service::SCON, &mut Command::new("/opt/orb/scon")
        .arg("mgr")
        // pass cmdline for console detection
        .args(&sys_info.cmdline))?;

    // ssh
    if DEBUG {
        // must use absolute path
        service_tracker.spawn(Service::SSH, &mut Command::new("/usr/sbin/sshd")
            .arg("-D") // foreground
            .arg("-e"))?; // log to stderr
    }

    Ok(())
}

pub async fn main(
    service_tracker: Arc<Mutex<ServiceTracker>>,
    action_tx: Sender<SystemAction>,
) -> Result<(), Box<dyn Error>> {
    let mut timeline = Timeline::new();
    let boot_start = Instant::now();

    timeline.begin("Booting OrbStack");

    // set basic environment
    timeline.begin("Set basic environment");
    set_basic_env()?;

    // pivot to overlayfs
    timeline.begin("Pivot to overlayfs");
    setup_overlayfs()?;

    // mount basic filesystems
    timeline.begin("Mount pseudo filesystems");
    mount_pseudo_fs()?;

    // system info
    // only works after pseudo-fs mounted
    let sys_info = SystemInfo::read()?;
    println!("  -  Kernel version: {}", sys_info.kernel_version);

    timeline.begin("Set up binfmt");
    setup_binfmt(&sys_info)?;

    timeline.begin("Set up network");
    setup_network().await?;

    timeline.begin("Start control server");
    tokio::spawn(vcontrol::server_main(action_tx.clone()));

    timeline.begin("Set clock");
    sync_clock(true)?;

    // do the following 3 slow stages in parallel
    // speedup: 300-400 ms -> 250 ms
    timeline.begin("Late tasks");
    let mut tasks = vec![];
    tasks.push(std::thread::spawn(|| { // 150 ms (w/o kernel hack to default to "none" iosched)
        //let stage_start = Instant::now();
        println!("     [*] Apply system settings");
        apply_perf_tuning_late().unwrap();
        //println!("     ... Applying system settings: +{}ms", stage_start.elapsed().as_millis());
    }));
    let sys_info_clone = sys_info.clone();
    tasks.push(std::thread::spawn(move || { // 50 ms
        //let stage_start = Instant::now();
        resize_data(&sys_info_clone).unwrap();

        println!("     [*] Mount data");
        mount_data().unwrap();
        //println!("     ... Mounting data: +{}ms", stage_start.elapsed().as_millis());
    }));
    // async, no need to wait for this
    std::thread::spawn(|| { // 70 ms
        //let stage_start = Instant::now();
        println!("     [*] Set up memory");
        setup_memory().unwrap();
        //println!("     ... Setting up memory: +{}ms", stage_start.elapsed().as_millis());
    });
    for task in tasks {
        task.join().unwrap();
    }

    timeline.begin("Initialize data");
    init_data()?;

    timeline.begin("Start services");
    start_services(service_tracker.clone(), &sys_info).await?;

    timeline.begin("Booted!");

    println!("  -  Total boot time: {}ms", boot_start.elapsed().as_millis());

    Ok(())
}
