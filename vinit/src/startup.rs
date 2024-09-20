use std::{
    cmp::Ordering,
    env,
    fs::{self, OpenOptions, Permissions},
    io::Write,
    net::UdpSocket,
    os::{
        fd::AsRawFd,
        unix::{
            fs::chroot,
            prelude::{FileExt, PermissionsExt},
        },
    },
    process::{Command, Stdio},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

use elf::{endian::NativeEndian, ElfStream};
use futures::StreamExt;
use futures_util::TryStreamExt;
use mkswap::SwapWriter;
use netlink_packet_core::{
    NetlinkMessage, NetlinkPayload, NLM_F_ACK, NLM_F_CREATE, NLM_F_REPLACE, NLM_F_REQUEST,
};
use netlink_packet_route::{
    rule::RuleAction,
    tc::{TcAttribute, TcHandle, TcMessage},
    RouteNetlinkMessage,
};
use nix::{
    libc::{self, RLIM_INFINITY, STDERR_FILENO, STDIN_FILENO, STDOUT_FILENO},
    mount::MsFlags,
    sys::{
        resource::{setrlimit, Resource},
        stat::{umask, Mode},
        time::TimeSpec,
    },
    time::{clock_gettime, clock_settime, ClockId},
    unistd::{dup2, sethostname},
};
use tokio::sync::mpsc::Sender;
use tokio::sync::Mutex as TMutex;
use tracing::log::debug;

use crate::{
    action::SystemAction,
    blockdev,
    filesystem::DiskManager,
    helpers::{
        sysctl, SWAP_FLAG_DISCARD, SWAP_FLAG_PREFER, SWAP_FLAG_PRIO_MASK, SWAP_FLAG_PRIO_SHIFT,
    },
    memory, vcontrol, InitError, SystemInfo, Timeline, DEBUG,
};
use crate::{
    filesystem::FsType,
    service::{Service, ServiceTracker},
};

// da:9b:d0:64:e1:01
const VNET_LLADDR: &[u8] = &[0xda, 0x9b, 0xd0, 0x64, 0xe1, 0x01];
const VNET_NEIGHBORS: &[&str] = &[
    "198.19.248.1",
    "198.19.248.200",
    "198.19.248.201",
    "198.19.248.253",
    "198.19.248.254",
    // only one IPv6: others are on ext subnet (to avoid NDP)
    "fd07:b51a:cc66:f0::1",
];

// destination for return packets:
// da:9b:d0:54:e1:02 (SconHostBridgeMAC)
const NAT64_SOURCE_LLADDR: &[u8] = &[0xda, 0x9b, 0xd0, 0x54, 0xe1, 0x02];
const NAT64_SOURCE_ADDR: &str = "10.183.233.241";
const NAT64_FWMARK: u32 = 0xe97bd031;

const FS_CORRUPTED_MSG: &str = r#"
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!! DATA IS LIKELY CORRUPTED.
!! Please make a backup, consider reporting this issue at https://orbstack.dev/issues/bug, and delete OrbStack data to continue.
!!
!! Giving up and shutting down now.
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
"#;

const DATA_DEV: &str = "/dev/vdb1";
const BTRFS_OPTIONS: &str = "discard,space_cache=v2,ssd,nodatacow,nodatasum,quota_statfs";

// binfmt magics
const ELF_MAGIC_X86_64: &str =
    r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00"#;
// the 3 bytes after ELF-02-01-01-00 are: ABI version (0x00) and 7 bytes of padding
// AppImage puts 0x414902 magic there: ABI version = 0x41, first 2 padding bytes = 0x49, 0x02
// union of (0x41 | 0x49 | 0x02), inverted, is 0x34
// this makes the matching as strict as possible while letting these magic bytes through
const ELF_MASK_X86_64: &str =
    r#"\xff\xff\xff\xff\xff\xfe\xfe\x00\x34\x34\x34\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#;

fn set_basic_env() -> anyhow::Result<()> {
    // umask: self write, others read
    umask(Mode::from_bits_truncate(0o022));

    // environment variables
    env::set_var(
        "PATH",
        "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
    );
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

fn mount_common(
    source: &str,
    dest: &str,
    fstype: Option<&str>,
    flags: MsFlags,
    data: Option<&str>,
) -> anyhow::Result<()> {
    if let Err(e) = nix::mount::mount(Some(source), dest, fstype, flags, data) {
        return Err(InitError::Mount {
            source: source.to_string(),
            dest: dest.to_string(),
            error: e,
        }
        .into());
    }

    Ok(())
}

fn mount(
    source: &str,
    dest: &str,
    fstype: &str,
    flags: MsFlags,
    data: Option<&str>,
) -> anyhow::Result<()> {
    mount_common(source, dest, Some(fstype), flags, data)
}

fn bind_mount(source: &str, dest: &str, flags: Option<MsFlags>) -> anyhow::Result<()> {
    mount_common(
        source,
        dest,
        None,
        flags.unwrap_or(MsFlags::empty()) | MsFlags::MS_BIND,
        None,
    )
}

fn bind_mount_ro(source: &str, dest: &str) -> anyhow::Result<()> {
    bind_mount(source, dest, None)?;
    // then we have to remount as ro with MS_REMOUNT | MS_BIND | MS_RDONLY
    bind_mount(dest, dest, Some(MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY))?;
    Ok(())
}

fn seal_read_only(path: &str) -> anyhow::Result<()> {
    // prevents machines from reopening /proc/<agent>/exe as writable. CVE-2019-5736
    bind_mount_ro(path, path)
}

fn setup_overlayfs() -> anyhow::Result<()> {
    let merged_flags = MsFlags::MS_NOATIME;
    // secure flags for overlay
    // this is used for Docker rootfs so don't pass "noexec". or people can't install packages in Docker machine
    // also don't pass nodev or overlayfs whiteouts won't work
    let upper_flags = merged_flags | MsFlags::MS_NOSUID;
    mount("tmpfs", "/run", "tmpfs", upper_flags, None)?;
    // create directories
    fs::create_dir_all("/run/upper")?;
    fs::create_dir_all("/run/work")?;
    fs::create_dir_all("/run/merged")?;
    // mount overlayfs - with vanity name for "df"
    mount(
        "orbstack",
        "/run/merged",
        "overlay",
        merged_flags,
        Some("lowerdir=/,upperdir=/run/upper,workdir=/run/work"),
    )?;

    // make original fs available for debugging
    if DEBUG {
        fs::create_dir_all("/run/merged/orig/run")?;
        bind_mount("/run", "/run/merged/orig/run", None)?;
        fs::create_dir_all("/run/merged/orig/root")?;
        bind_mount("/", "/run/merged/orig/root", None)?;
    }

    // switch root
    /*
    equivalent to:
        cd /run/merged
        mount --move . /
        chroot .
    */
    env::set_current_dir("/run/merged")?;
    mount_common(".", "/", None, MsFlags::MS_MOVE, None)?;
    chroot(".")?;

    Ok(())
}

fn mount_pseudo_fs() -> anyhow::Result<()> {
    let secure_flags =
        MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID | MsFlags::MS_NODEV | MsFlags::MS_RELATIME;
    let dev_flags = MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID | MsFlags::MS_RELATIME;
    // easier for dev to allow exec
    let tmp_flags = MsFlags::MS_NOSUID | MsFlags::MS_NODEV | MsFlags::MS_RELATIME;

    // essential
    mount("sysfs", "/sys", "sysfs", secure_flags, None)?;
    apply_perf_tuning_early()?;
    mount("proc", "/proc", "proc", secure_flags, None)?;
    // disable quiet after kernel boot completed
    fs::write("/proc/sys/kernel/printk", "7")?;
    mount("devtmpfs", "/dev", "devtmpfs", dev_flags, Some("mode=0755"))?;
    // extra
    fs::create_dir_all("/dev/pts")?;
    mount(
        "devpts",
        "/dev/pts",
        "devpts",
        dev_flags,
        Some("mode=0620,gid=5,ptmxmode=000"),
    )?;
    mount(
        "securityfs",
        "/sys/kernel/security",
        "securityfs",
        secure_flags,
        None,
    )?;
    mount(
        "debugfs",
        "/sys/kernel/debug",
        "debugfs",
        secure_flags,
        None,
    )?;
    fs::create_dir_all("/dev/mqueue")?;
    mount("mqueue", "/dev/mqueue", "mqueue", secure_flags, None)?;
    mount(
        "fusectl",
        "/sys/fs/fuse/connections",
        "fusectl",
        secure_flags,
        None,
    )?;
    mount(
        "binfmt_misc",
        "/proc/sys/fs/binfmt_misc",
        "binfmt_misc",
        secure_flags,
        None,
    )?;
    mount(
        "tracefs",
        "/sys/kernel/tracing",
        "tracefs",
        secure_flags,
        None,
    )?;
    mount("bpf", "/sys/fs/bpf", "bpf", secure_flags, Some("mode=0700"))?;
    // tmp
    fs::create_dir_all("/dev/shm")?;
    mount("shm", "/dev/shm", "tmpfs", secure_flags, Some("mode=1777"))?;
    mount("tmpfs", "/run", "tmpfs", secure_flags, Some("mode=0755"))?;
    mount("tmpfs", "/tmp", "tmpfs", tmp_flags, Some("mode=0755"))?;

    // cgroup2 (nsdelegate for delegation/confinement)
    mount(
        "cgroup",
        "/sys/fs/cgroup",
        "cgroup2",
        secure_flags,
        Some("nsdelegate"),
    )?;
    // enable all controllers for subgroups
    let subtree_controllers = fs::read_to_string("/sys/fs/cgroup/cgroup.controllers")?
        .trim()
        .split(' ')
        // prepend '+' to each controller
        .map(|s| "+".to_string() + s)
        .collect::<Vec<String>>()
        .join(" ");
    fs::write("/sys/fs/cgroup/cgroup.subtree_control", subtree_controllers)?;

    // nfsd
    mount("nfsd", "/proc/fs/nfsd", "nfsd", secure_flags, None)?;
    // to prevent EBUSY, set options before starting anything
    // set lease time to 5 years:
    //   - kernel math's max value is (lease_time * 1e9 * 2) in 64 bits, so limit is 9223372036
    //     * 5 years = 157680000 - is safe
    //   - days/weeks is too short: what if the renew timer fires in sleep and causes timeout?
    //     * fundamental problem: kernel can run when userspace can't
    //     * non-issue for standard nfs, because kernel sends network req out to remote server, which is running
    //     * unavoidable issue for local nfs, because userspace can't be running
    //     * but very unlikely to have random fs request while userspace can't run - could have renew timer though
    //   - if this breaks and gets a lock stuck, there's little diff between 6 months and 5 years (to the user)
    //   - getting rid of renew timer saves CPU and reduces chances of unmount during sleep
    //   - is safe because we only have one client
    //   - 30 sec grace period is safe, but wasteful, and already bad enough UX if we end up hanging for 30s
    fs::write(
        "/proc/fs/nfsd/nfsv4leasetime",
        format!("{}", 5 * 365 * 24 * 3600),
    )?;
    fs::write("/proc/fs/nfsd/nfsv4gracetime", "1")?;

    // for security, seal all directories/files we expose to machines as read-only
    // otherwise machines can remount them as read-write
    seal_read_only("/opt/orb")?;

    // early race-free emulator setup on arm64
    #[cfg(target_arch = "aarch64")]
    setup_arch_emulators_early()?;

    Ok(())
}

fn apply_perf_tuning_early() -> anyhow::Result<()> {
    // expedited RCU
    // speeds up container startup ~2x:
    // machine startup 4x: 260 -> 40 ms
    // low cost in practice (no IPI for idle CPUs): https://docs.kernel.org/RCU/Design/Expedited-Grace-Periods/Expedited-Grace-Periods.html
    // do it here instead of kernel to make it less obvious. as early as possible in userspace
    fs::write("/sys/kernel/rcu_expedited", "1")?;

    // mTHP: multi-size THP, aka large anon folios
    // on fault, if eligible, kernel will always round address down and try to allocate order-2 (16K) folios on 4K-page systems
    // good for perf in lieu of 16K pages, but main purpose is to reduce 4K-16K fragmentation for balloon on arm64
    // do it early to minimize 4K allocations on boot
    // fail gracefully if THP is off
    _ = fs::write(
        "/sys/kernel/mm/transparent_hugepage/hugepages-16kB/enabled",
        "always",
    );

    // balloon free page reporting order
    // should be equivalent to host page size (16K), as that's the freeable page granule
    // this is order=2 on 4K kernels and order=0 on 16K kernels
    // set it here to avoid exposing it in kernel cmdline
    // it's ok that this isn't set early enough for the page_reporting static branch gate: something will be freed soon
    let page_size = unsafe { libc::sysconf(libc::_SC_PAGESIZE) as usize };
    let order_16k = (16384 / page_size).ilog2();
    fs::write(
        "/sys/module/page_reporting/parameters/page_reporting_order",
        order_16k.to_string(),
    )?;

    Ok(())
}

fn apply_perf_tuning_late() -> anyhow::Result<()> {
    // reduce idle cpu usage
    sysctl("vm.compaction_proactiveness", "0")?;
    sysctl("vm.stat_interval", "30")?;
    sysctl("vm.dirty_writeback_centisecs", "1500")?;

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

    // k8s / k3s
    sysctl("vm.panic_on_oom", "0")?;
    sysctl("kernel.panic_on_oops", "1")?;
    sysctl("net.netfilter.nf_conntrack_max", "327680")?;
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

    Ok(())
}

async fn setup_network() -> anyhow::Result<()> {
    // don't send IPv6 router solicitations
    sysctl("net.ipv6.conf.all.accept_ra", "0")?;
    sysctl("net.ipv6.conf.default.accept_ra", "0")?;
    // and fix tentative IPv6 delay
    sysctl("net.ipv6.conf.eth0.accept_dad", "0")?;
    // on all interfaces, e.g. conbr0 too
    // TODO consider optimistic dad
    sysctl("net.ipv6.conf.all.accept_dad", "0")?;
    sysctl("net.ipv6.conf.default.accept_dad", "0")?;

    // scon net
    sysctl("net.ipv4.ip_forward", "1")?;
    sysctl("net.ipv6.conf.all.forwarding", "1")?;

    // connect to rtnetlink
    let (conn, mut handle, _) = rtnetlink::new_connection()?;
    let conn_task = tokio::spawn(conn);
    let mut ip_link = handle.link();
    let ip_addr = handle.address();
    let ip_route = handle.route();
    let ip_rule = handle.rule();
    let ip_neigh = handle.neighbours();

    // loopback: set lo up
    let lo = ip_link
        .get()
        .match_name("lo".into())
        .execute()
        .try_next()
        .await?
        .unwrap();
    ip_link.set(lo.header.index).up().execute().await?;

    // main gvisor NAT network
    let eth0 = ip_link
        .get()
        .match_name("eth0".into())
        .execute()
        .try_next()
        .await?
        .unwrap();

    // static neighbors to reduce ARP CPU usage
    for ip_addr in VNET_NEIGHBORS {
        ip_neigh
            .add(eth0.header.index, ip_addr.parse()?)
            .link_local_address(VNET_LLADDR)
            .execute()
            .await?;
    }

    // set eth0 mtu, up
    ip_link
        .set(eth0.header.index)
        .mtu(1500)
        .up()
        .execute()
        .await?;

    // add IP addresses
    ip_addr
        .add(eth0.header.index, "198.19.248.2".parse()?, 24)
        .execute()
        .await?;
    // to avoid NDP, use /126 so only ::1 and ::2 are on the network
    ip_addr
        .add(eth0.header.index, "fd07:b51a:cc66:f0::2".parse()?, 126)
        .execute()
        .await?;

    // add default routes
    ip_route
        .add()
        .v4()
        .gateway("198.19.248.1".parse()?)
        .execute()
        .await?;
    ip_route
        .add()
        .v6()
        .gateway("fd07:b51a:cc66:f0::1".parse()?)
        .execute()
        .await?;

    // scon machine bridge: eth1 mtu, up
    // scon deals with the rest
    // cannot use static neigh because macOS generates MAC addr
    let eth1 = ip_link
        .get()
        .match_name("eth1".into())
        .execute()
        .try_next()
        .await?
        .unwrap();
    ip_link
        .set(eth1.header.index)
        .mtu(1500)
        .up()
        .execute()
        .await?;

    // NAT64 from machine bridge to docker
    // make Linux happy (doesn't really matter)
    ip_neigh
        .add(eth1.header.index, NAT64_SOURCE_ADDR.parse().unwrap())
        .link_local_address(NAT64_SOURCE_LLADDR)
        .execute()
        .await
        .unwrap();
    // ingress route from translated IPv4 source address to Docker machine (which does IP forward to containers)
    // create ip rule for fwmark from BPF clsact program
    // ip rule add fwmark 0xe97bd031 table 64
    ip_rule
        .add()
        .v4()
        .table_id(64) // table ID is not exposed to BPF
        .fw_mark(NAT64_FWMARK)
        .action(RuleAction::ToTable)
        .execute()
        .await?;
    // ip route add default via 198.19.249.2 table 64
    // ip_route.add().v4()
    //     .gateway(NAT64_DOCKER_MACHINE_IP4.parse().unwrap())
    //     .table(64).execute().await.unwrap();
    // egress route from Docker machine back to BPF eth1
    // ip route add 10.183.233.241 dev eth1
    ip_route
        .add()
        .v4()
        .destination_prefix(NAT64_SOURCE_ADDR.parse().unwrap(), 32)
        .output_interface(eth1.header.index)
        .execute()
        .await
        .unwrap();

    // docker vlan router
    // scon deals with the rest
    let eth2 = ip_link
        .get()
        .match_name("eth2".into())
        .execute()
        .try_next()
        .await?
        .unwrap();
    ip_link
        .set(eth2.header.index)
        .mtu(1500)
        .up()
        .execute()
        .await?;

    // set qdisc on all physical interfaces
    for eth in &[eth0, eth1, eth2] {
        let mut msg = TcMessage::with_index(eth.header.index as i32);
        msg.header.parent = TcHandle::ROOT;
        msg.header.handle = TcHandle::UNSPEC;
        msg.attributes
            .push(TcAttribute::Kind("fq_codel".to_string()));
        let mut req = NetlinkMessage::from(RouteNetlinkMessage::NewQueueDiscipline(msg));
        req.header.flags = NLM_F_REQUEST | NLM_F_ACK | NLM_F_CREATE | NLM_F_REPLACE;

        let mut response = handle.request(req)?;
        while let Some(message) = response.next().await {
            if let NetlinkPayload::Error(err) = message.payload {
                return Err(rtnetlink::Error::NetlinkError(err).into());
            }
        }
    }

    conn_task.abort();
    Ok(())
}

pub fn sync_clock(allow_backward: bool) -> anyhow::Result<()> {
    // sync clock immediately at boot (if RTC is wrong) or on wake (until chrony kicks in)
    // RTC can supposedly be wrong at boot: https://news.ycombinator.com/item?id=36185786
    let socket = UdpSocket::bind("0.0.0.0:0")?;
    socket.set_read_timeout(Some(Duration::from_secs(10)))?;
    let host_time =
        sntpc::simple_get_time("198.19.248.200:123", socket).map_err(InitError::NtpGetTime)?;

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

    Ok(())
}

fn resize_data(sys_info: &SystemInfo) -> anyhow::Result<()> {
    // resize data partition
    // scon resizes the filesystem
    let new_size_mib = sys_info.seed.data_size_mib;
    // get existing size
    let old_size_mib =
        blockdev::getsize64(DATA_DEV).map_err(InitError::MissingDataPartition)? / 1024 / 1024;

    // for safety, only allow increasing size
    match new_size_mib.cmp(&old_size_mib) {
        Ordering::Greater => {
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
        }
        Ordering::Less => {
            eprintln!(
                "WARNING: Attempted to shrink data partition from {} MiB to {} MiB",
                old_size_mib, new_size_mib
            );
        }
        // normal: we always call this
        Ordering::Equal => {}
    }

    Ok(())
}

fn mount_data(sys_info: &SystemInfo, disk_manager: &Mutex<DiskManager>) -> anyhow::Result<()> {
    // virtiofs share
    mount("mac", "/mnt/mac", "virtiofs", MsFlags::MS_RELATIME, None)?;

    // data
    // first try with regular mount, then try usebackuproot
    let data_flags = MsFlags::MS_NOATIME;
    let fs_type = FsType::detect(DATA_DEV)?;
    match fs_type {
        FsType::Btrfs => {
            if let Err(e) = mount(DATA_DEV, "/data", "btrfs", data_flags, Some(BTRFS_OPTIONS)) {
                eprintln!(" !!! Failed to mount data: {}", e);
                println!(" [*] Attempting to recover data");
                if let Err(e) = mount(
                    DATA_DEV,
                    "/data",
                    "btrfs",
                    data_flags,
                    Some(format!("{},rescue=usebackuproot", BTRFS_OPTIONS).as_str()),
                ) {
                    eprintln!(" !!! Failed to recover data: {}", e);
                    eprintln!("{}", FS_CORRUPTED_MSG);
                    return Err(e);
                }
            }
        }

        FsType::Bcachefs => {
            mount(DATA_DEV, "/data", "bcachefs", data_flags, Some("discard")).unwrap();
        }

        FsType::Xfs => {
            mount(
                DATA_DEV,
                "/data",
                "xfs",
                data_flags,
                Some("prjquota,discard"),
            )
            .unwrap();
        }

        FsType::Ext4 => {
            mount(
                DATA_DEV,
                "/data",
                "ext4",
                data_flags,
                Some("prjquota,discard"),
            )
            .unwrap();
        }

        FsType::F2fs => {
            mount(
                DATA_DEV,
                "/data",
                "f2fs",
                data_flags,
                Some("prjquota,discard"),
            )
            .unwrap();
        }
    }

    disk_manager
        .lock()
        .unwrap()
        .update_with_stats(&sys_info.seed.initial_disk_stats)
        .unwrap();

    Ok(())
}

fn create_noindex_flags(dir: &str) -> anyhow::Result<()> {
    // attempt to reduce NFS CPU usage from macOS indexing
    fs::write(format!("{}/.metadata_never_index", dir), "").unwrap();
    fs::write(format!("{}/.metadata-never-index", dir), "").unwrap();
    fs::write(format!("{}/.metadata_never_index_unless_rootfs", dir), "").unwrap();
    // and from fsevents logs
    fs::create_dir_all(format!("{}/.fseventsd", dir)).unwrap();
    fs::write(format!("{}/.fseventsd/no_log", dir), "").unwrap();

    Ok(())
}

// a mirror dirs is a tmpfs dir with ro and rw binds, meant for exporting over nfs (possibly as subdir)
fn create_mirror_dir(dir: &str) -> anyhow::Result<(String, String)> {
    let ro_dir = format!("{}/ro", dir);
    let rw_dir = format!("{}/rw", dir);

    fs::create_dir_all(&ro_dir).unwrap();
    fs::create_dir_all(&rw_dir).unwrap();

    // create noindex flags
    create_noindex_flags(&rw_dir).unwrap();

    // seal ro copy:
    // read-only bind (+ rshared, for scon bind mounts)
    bind_mount_ro(&rw_dir, &ro_dir)?;
    // and finally, make it shared (doesn't work as flag in above calls)
    mount_common(&ro_dir, &ro_dir, None, MsFlags::MS_SHARED, None).unwrap();
    Ok((ro_dir, rw_dir))
}

fn init_nfs() -> anyhow::Result<()> {
    // mount name is visible in machines bind mount, so use vanity name
    // we use this same tmpfs for all mirror dirs
    mount(
        "machines",
        "/nfs",
        "tmpfs",
        MsFlags::MS_NOATIME | MsFlags::MS_NODEV | MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID,
        Some("mode=0755"),
    )
    .unwrap();

    // create mirror dirs: root, images, containers
    // perf matters more for volumes so it uses raw binds instead of mergerfs
    let (_, rw_root) = create_mirror_dir("/nfs/root").unwrap();
    let (_, rw_for_machines) = create_mirror_dir("/nfs/for-machines").unwrap();
    _ = create_mirror_dir("/nfs/containers").unwrap();

    // readme in root
    fs::copy("/opt/orb/nfs-readme.txt", format!("{}/README.txt", rw_root)).unwrap();

    // create mergerfs and volume mountpoint dirs
    fs::create_dir_all(format!("{}/docker/volumes", rw_root)).unwrap();
    fs::create_dir_all(format!("{}/docker/images", rw_root)).unwrap();
    fs::create_dir_all(format!("{}/docker/containers", rw_root)).unwrap();
    fs::create_dir_all(format!("{}/docker/volumes", rw_for_machines)).unwrap();
    fs::create_dir("/tmp/empty").unwrap();

    Ok(())
}

// btrfs COW means that setting perms requires a new metadata block
// that's not possible if qgroup is set to exactly used, or negative, resulting in ENOSPC on boot
// so avoid changing perms if not necessary
fn maybe_set_permissions(path: &str, mode: u32) -> anyhow::Result<()> {
    // only set if it's different
    let current_mode = fs::metadata(path)?.permissions().mode();
    if (current_mode & 0o777) != mode {
        fs::set_permissions(path, Permissions::from_mode(mode))?;
    }
    Ok(())
}

fn init_data() -> anyhow::Result<()> {
    // guest tools
    fs::create_dir_all("/data/guest-state/bin/cmdlinks")?;
    maybe_set_permissions("/data/guest-state/bin", 0o755)?;
    maybe_set_permissions("/data/guest-state/bin/cmdlinks", 0o755)?;
    bind_mount("/data/guest-state", "/opt/orbstack-guest/data", None)?;

    // wormhole overlay
    fs::create_dir_all("/data/wormhole/overlay/upper")?;
    fs::create_dir_all("/data/wormhole/overlay/work")?;
    // mount a r-o nix to protect /nix/orb/sys and prevent creating files in /nix/.
    bind_mount_ro("/opt/wormhole-rootfs", "/mnt/wormhole-unified")?;
    // expose read-only base store
    bind_mount_ro(
        "/opt/wormhole-rootfs/nix/store",
        "/mnt/wormhole-unified/nix/orb/sys/.base",
    )?;

    // debug root home
    if DEBUG {
        fs::create_dir_all("/data/dev-root-home/.ssh")?;
        fs::copy(
            "/root/.ssh/authorized_keys",
            "/data/dev-root-home/.ssh/authorized_keys",
        )?;
        maybe_set_permissions("/data/dev-root-home/.ssh", 0o700)?;
        maybe_set_permissions("/data/dev-root-home/.ssh/authorized_keys", 0o600)?;
        bind_mount("/data/dev-root-home", "/root", None)?;
    }

    // set up NFS roots for scon
    init_nfs()?;

    Ok(())
}

fn add_binfmt(
    name: &str,
    magic: &str,
    mask: Option<&str>,
    interpreter: &str,
    flags: &str,
) -> anyhow::Result<()> {
    let offset = 0;
    let buf = format!(
        ":{}:M:{}:{}:{}:{}:{}",
        name,
        offset,
        magic,
        mask.unwrap_or(""),
        interpreter,
        flags
    );
    fs::write("/proc/sys/fs/binfmt_misc/register", buf)?;
    Ok(())
}

#[cfg(target_arch = "x86_64")]
fn setup_arch_emulators(_sys_info: &SystemInfo) -> anyhow::Result<()> {
    // arm64 qemu
    add_binfmt(
        "qemu-aarch64",
        r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xb7\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#),
        "[qemu-arm64]",
        "POCF",
    )?;
    Ok(())
}

#[cfg(target_arch = "aarch64")]
fn prepare_rosetta_bin() -> anyhow::Result<bool> {
    use crate::rosetta::{self, RosettaError};

    // create tmpfs that allows exec
    mount("tmpfs", "/mnt/rv", "tmpfs", MsFlags::MS_NOATIME, None).unwrap();

    // copy rosetta binary
    fs::copy("/mnt/rosetta/rosetta", "/mnt/rv/[rosetta]").unwrap();
    fs::set_permissions("/mnt/rv/[rosetta]", Permissions::from_mode(0o755)).unwrap();

    // apply patch
    let mut patched = false;
    let source_data = fs::read("/mnt/rv/[rosetta]").unwrap();
    match rosetta::find_and_apply_patch(&source_data, "/mnt/rv/[rosetta]") {
        Ok(_) => {
            patched = true;
        }
        Err(RosettaError::UnknownBuild(fingerprint)) => {
            // allow proceeding, but try to print the version
            // rvfs isn't ready yet so run from virtiofs
            let version = rosetta::get_version("/mnt/rosetta/rosetta")
                .unwrap_or_else(|e| format!("unknown ({}) ({})", &fingerprint[..8], e));
            eprintln!("  !  Unknown Rosetta version: {}", version);
        }
        Err(e) => return Err(e.into()),
    }

    // remount readonly
    mount(
        "tmpfs",
        "/mnt/rv",
        "tmpfs",
        MsFlags::MS_REMOUNT | MsFlags::MS_NOATIME | MsFlags::MS_RDONLY,
        None,
    )
    .unwrap();

    // redirect ioctls to real rosetta virtiofs
    let real_rosetta_file = fs::File::open("/mnt/rosetta/rosetta").unwrap();
    let new_file = fs::File::open("/mnt/rv/[rosetta]").unwrap();
    rosetta::adopt_rvfs_files(real_rosetta_file, new_file).unwrap();

    // we're done setting up the new rosetta.
    // wrapper doesn't need any special treatment because it uses comm=rvk1/rvk2 keys
    Ok(patched)
}

#[cfg(target_arch = "aarch64")]
fn prepare_rstub(host_build: &str) -> anyhow::Result<()> {
    use crate::rosetta::RSTUB_FLAG_TSO_WORKAROUND;

    // copy rstub binary
    fs::copy("/opt/orb/rstub", "/tmp/rstub")?;
    fs::set_permissions("/tmp/rstub", Permissions::from_mode(0o755))?;

    // parse elf
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .open("/tmp/rstub")?;
    let mut elf = ElfStream::<NativeEndian, _>::open_stream(&file)?;

    // get config block
    let shdr = elf
        .section_header_by_name(".c0")?
        .ok_or(InitError::InvalidElf)?;
    // get offset in file
    let cfg_offset = shdr.sh_offset;

    // create config
    let mut flags = 0u32;
    if host_build.starts_with("23A") {
        // macOS 14.0.x has broken TSO
        // fixed in 14.1 RC (23B73)
        flags |= RSTUB_FLAG_TSO_WORKAROUND;

        // careful with this workaround.
        // it can break stuff, I guess because nproc returns 1.
        // example: `docker run -it --rm -e CEPH_PUBLIC_NETWORK=0.0.0.0/0 -e MON_IP=127.0.0.1 -e CEPH_DEMO_UID=demo quay.io/ceph/demo:latest-quincy` gets stuck at `changed ownership of '/var/lib/ceph/osd/ceph-0' from root:root to ceph:ceph`
        // also (allegedly): https://github.com/orbstack/orbstack/issues/730
    }

    // write 32-bit config flags to section
    file.write_all_at(&flags.to_le_bytes(), cfg_offset)?;

    Ok(())
}

#[cfg(target_arch = "aarch64")]
fn setup_arch_emulators_early() -> anyhow::Result<()> {
    // install a dummy to prevent the native architecture from being emulated
    // MUST BE EARLY, or we could break execs sometimes when racing with other steps
    // this is the name used by ubuntu binfmt
    // also happens with: docker run --rm --privileged multiarch/qemu-user-static:register
    add_binfmt(
        "qemu-aarch64",
        r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xb7\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#),
        "[qemu]",
        "POCF",
    )?;
    // then disable the entry. it's just there to take the name
    fs::write("/proc/sys/fs/binfmt_misc/qemu-aarch64", "0")?;

    Ok(())
}

#[cfg(target_arch = "aarch64")]
fn setup_arch_emulators(sys_info: &SystemInfo) -> anyhow::Result<()> {
    // we always register qemu, but flags change if using Rosetta
    let mut qemu_flags = "POCF".to_string();

    if mount(
        "rosetta",
        "/mnt/rosetta",
        "virtiofs",
        MsFlags::empty(),
        None,
    )
    .is_ok()
    {
        // rosetta
        println!("  -  Using Rosetta");

        // copy to rvfs and apply delta
        let patched = prepare_rosetta_bin().unwrap();

        // add preserve-argv0 flag on Sonoma Rosetta 309+
        let mut rosetta_flags = "CF(".to_string();
        if patched || sys_info.seed.host_major_version >= 14 {
            rosetta_flags += "P"
        }

        // prepare rosetta wrapper
        prepare_rstub(&sys_info.seed.host_build_version).unwrap();

        // if we're using Rosetta, we'll do it through the RVFS wrapper.
        // add flag to register qemu-x86_64 as a hidden handler that the RVFS wrapper can use, via comm=rvk2
        qemu_flags += ")"; // MISC_FMT_ORBRVK2

        // register RVFS wrapper first
        // entries added later take priority, so MUST register first to avoid infinite loop
        // WARNING: NOT THREAD SAFE! this uses chdir.
        //          luckily init doesn't care about cwd during early boot (but later, it matters for spawned processes)
        add_binfmt(
            "rosetta",
            ELF_MAGIC_X86_64,
            Some(ELF_MASK_X86_64),
            "[rosetta]",
            "POCF",
        )
        .unwrap();

        // then register real rosetta with comm=rvk1 key '('
        // '.' to make it hidden
        env::set_current_dir("/mnt/rv").unwrap();
        // use zero-width spaces to make it hard to inspect
        let real_res = add_binfmt(
            "rosetta\u{200b}",
            ELF_MAGIC_X86_64,
            Some(ELF_MASK_X86_64),
            "[rosetta]",
            &rosetta_flags,
        );
        // rvk3 variant without preserve-argv0 flag, to work around bug for swift-driver
        let real_res2 = add_binfmt(
            "rosetta\u{200b}\u{200b}",
            ELF_MAGIC_X86_64,
            Some(ELF_MASK_X86_64),
            "[rosetta]",
            "CF[",
        );
        env::set_current_dir("/").unwrap();
        real_res.unwrap();
        real_res2.unwrap();
    } else {
        // qemu
        println!("  -  Using QEMU");
    }

    // always register qemu x86_64
    // if Rosetta mode: RVFS wrapper may choose to invoke it via task comm=rvk2 key (we add ')' flag)
    // if QEMU mode: it will always be used
    // this also helps occupy the name so that distros don't try to install it
    add_binfmt(
        "qemu-x86_64",
        ELF_MAGIC_X86_64,
        Some(ELF_MASK_X86_64),
        "[qemu]",
        &qemu_flags,
    )?;

    // always use qemu for i386 (32-bit)
    // Rosetta doesn't support 32-bit
    add_binfmt(
        "qemu-i386",
        r#"\x7f\x45\x4c\x46\x01\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x03\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\xfc\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#),
        "[qemu32]",
        "POCF",
    )?;

    Ok(())
}

fn setup_binfmt(sys_info: &SystemInfo) -> anyhow::Result<()> {
    setup_arch_emulators(sys_info)?;

    // qemu for 32-bit ARM
    // must be emulated on both x86 and arm64
    // all our qemus use standard names to avoid distro conflicts in case user tries to install them
    add_binfmt(
        "qemu-arm",
        r#"\x7f\x45\x4c\x46\x01\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x28\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#),
        "[qemu-arm32]",
        "POCF",
    )?;

    // other common qemus: riscv64, ppc64le, s390x, mips64el
    add_binfmt(
        "qemu-riscv64",
        r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xf3\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#),
        "[qemu-riscv64]",
        "POCF",
    )?;
    add_binfmt(
        "qemu-ppc64le",
        r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x15\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\x00"#),
        "[qemu-ppc64le]",
        "POCF",
    )?;
    add_binfmt(
        "qemu-s390x",
        r#"\x7fELF\x02\x02\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x16"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff"#),
        "[qemu-s390x]",
        "POCF",
    )?;
    add_binfmt(
        "qemu-mips64el",
        r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x08\x00"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x00\x00\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#),
        "[qemu-mips64el]",
        "POCF",
    )?;

    // Mach-O
    // no O because fds can't cross OS boundary
    // no C because credentials are ignored
    // macOS doesn't support 32-bit anymore
    // 07 = x86
    add_binfmt(
        "mac-macho-x86_64",
        r#"\xcf\xfa\xed\xfe\x07\x00\x00\x01"#,
        None,
        "[mac]",
        "FP",
    )?;
    // only for arm64
    #[cfg(target_arch = "aarch64")]
    add_binfmt(
        "mac-macho-arm64",
        r#"\xcf\xfa\xed\xfe\x0c\x00\x00\x01"#,
        None,
        "[mac]",
        "FP",
    )?;

    // macOS Universal (either arch first)
    // accepts both 1 and 2 binaries, with either arch first
    // no conflict with java: https://github.com/file/file/blob/c8bba134ac1f3c9f5b052486a7694c5b48e498bc/magic/Magdir/cafebabe#L3
    add_binfmt(
        "mac-universal-x86_64",
        r#"\xca\xfe\xba\xbe\x00\x00\x00\x02\x01\x00\x00\x07"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x02\xff\xff\xff\xff"#),
        "[mac]",
        "FP",
    )?;
    add_binfmt(
        "mac-universal-arm64",
        r#"\xca\xfe\xba\xbe\x00\x00\x00\x02\x01\x00\x00\x0c"#,
        Some(r#"\xff\xff\xff\xff\xff\xff\xff\x02\xff\xff\xff\xff"#),
        "[mac]",
        "FP",
    )?;

    Ok(())
}

fn enable_swap(path: &str, priority: i32) -> anyhow::Result<()> {
    unsafe {
        let path = std::ffi::CString::new(path)?;
        // allow discard to free zram pages
        let res = libc::swapon(
            path.as_ptr(),
            SWAP_FLAG_DISCARD
                | SWAP_FLAG_PREFER
                | (priority << SWAP_FLAG_PRIO_SHIFT) & SWAP_FLAG_PRIO_MASK,
        );
        if res != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }

    Ok(())
}

fn setup_memory() -> anyhow::Result<()> {
    // prevent us from getting swapped out in case of memory pressure
    // (~8 ms, so it's in this async task)
    // ... but it allocates ~100M of memory! way more than the RSS of ~27M for vinit
    // not worth it
    //mlockall(MlockAllFlags::MCL_CURRENT | MlockAllFlags::MCL_FUTURE)?;

    // sysctls
    sysctl("vm.overcommit_memory", "1")?;
    sysctl("vm.swappiness", "20")?;
    sysctl("vm.page-cluster", "1")?;
    sysctl("vm.watermark_boost_factor", "0")?;

    // MGLRU thrashing protection
    fs::write("/sys/kernel/mm/lru_gen/min_ttl_ms", "500")?;

    // zram
    // size = 1x RAM
    // no writeback
    let mem_total_kib = fs::read_to_string("/proc/meminfo")?
        .lines()
        .find(|l| l.starts_with("MemTotal:"))
        .unwrap()
        .split_whitespace()
        .nth(1)
        .unwrap()
        .parse::<u64>()?;
    fs::write(
        "/sys/block/zram0/disksize",
        format!("{}", mem_total_kib * 1024),
    )?;

    // create swap on zram
    let zram_dev = OpenOptions::new()
        .read(true)
        .write(true)
        .open("/dev/zram0")?;
    SwapWriter::new().write(zram_dev)?;
    // enable
    enable_swap("/dev/zram0", 32767)?;

    // emergency disk swap (1 GiB)
    let swap_dev = OpenOptions::new().read(true).write(true).open("/dev/vdc")?;
    SwapWriter::new().write(swap_dev)?;
    enable_swap("/dev/vdc", 1)?;

    Ok(())
}

async fn start_services(
    service_tracker: Arc<TMutex<ServiceTracker>>,
    sys_info: &SystemInfo,
) -> anyhow::Result<()> {
    let mut service_tracker = service_tracker.lock().await;

    // chrony
    service_tracker.spawn(
        Service::CHRONY,
        Command::new("/usr/sbin/chronyd")
            .arg(if DEBUG { "-d" } else { "-n" }) // foreground (-d for log-to-stderr)
            .arg("-f") // config file
            .arg("/etc/chrony/chrony.conf"),
    )?;

    // udev
    // this is only for USB devices, nbd, etc. so no need to wait for it to settle
    service_tracker.spawn(Service::UDEV, &mut Command::new("/sbin/udevd"))?;

    // scon
    service_tracker.spawn(
        Service::SCON,
        Command::new("/opt/orb/scon")
            .arg("mgr")
            // pass cmdline for console detection
            .arg(if sys_info.seed.console_is_pipe {
                "orb.console_is_pipe"
            } else {
                ""
            }),
    )?;

    // ssh
    if DEBUG {
        // must use absolute path for sshd's sandbox to work
        service_tracker.spawn(
            Service::SSH,
            Command::new("/usr/sbin/sshd")
                .arg("-D") // foreground
                .arg("-e"),
        )?; // log to stderr
    }

    Ok(())
}

fn switch_console(sys_info: &SystemInfo) -> anyhow::Result<()> {
    let console_path = &sys_info.seed.console_path;
    let console = OpenOptions::new()
        .read(true)
        .write(true)
        .open(console_path)?;

    // replace stdin, stdout, stderr
    // don't set CLOEXEC: these fds should be inherited
    let console_fd = console.as_raw_fd();
    dup2(console_fd, STDIN_FILENO)?;
    dup2(console_fd, STDOUT_FILENO)?;
    dup2(console_fd, STDERR_FILENO)?;

    Ok(())
}

pub async fn main(
    service_tracker: Arc<TMutex<ServiceTracker>>,
    action_tx: Sender<SystemAction>,
) -> anyhow::Result<()> {
    let mut timeline = Timeline::new();
    let boot_start = Instant::now();

    timeline.begin("Booting OrbStack");

    // set basic environment
    set_basic_env()?;
    // pivot to overlayfs
    setup_overlayfs()?;
    // mount basic filesystems
    mount_pseudo_fs()?;

    // system info
    // only works after pseudo-fs mounted
    let sys_info = SystemInfo::read()?;

    // switch userspace stdout console to vport as early as possible, to reduce CPU usage
    // ttys use spinlocks, so writing to hvc0 spinloops and blocks the vCPU if the host's pipe is full
    switch_console(&sys_info)?;

    println!("  -  Kernel version: {}", sys_info.kernel_version);

    timeline.begin("Network");
    setup_network().await?;
    // start control server
    let disk_manager = Arc::new(Mutex::new(DiskManager::new().unwrap()));
    tokio::spawn(vcontrol::server_main(
        disk_manager.clone(),
        action_tx.clone(),
    ));

    // very fast w/ kernel hack to default to "none" iosched for virtio-blk (150 ms without)
    timeline.begin("Apply system settings");
    // set clock
    sync_clock(true)?;
    // tune perf
    apply_perf_tuning_late().unwrap();

    // do the following 3 slow stages in parallel
    // speedup: 300-400 ms -> 250 ms
    timeline.begin("Late tasks");
    let mut tasks = vec![];
    let sys_info_clone = sys_info.clone();
    tasks.push(std::thread::spawn(move || {
        // 55 ms
        //let stage_start = Instant::now();
        println!("     [*] Emulation");
        setup_binfmt(&sys_info_clone).unwrap();
        //println!("     ... Set up binfmt: +{}ms", stage_start.elapsed().as_millis());
    }));
    let sys_info_clone = sys_info.clone();
    tasks.push(std::thread::spawn(move || {
        // 50 ms
        //let stage_start = Instant::now();
        resize_data(&sys_info_clone).unwrap();

        println!("     [*] Data");
        mount_data(&sys_info_clone, &disk_manager).unwrap();
        //println!("     ... Mounting data: +{}ms", stage_start.elapsed().as_millis());
    }));
    // async, no need to wait for this
    std::thread::spawn(|| {
        // 70 ms
        //let stage_start = Instant::now();
        println!("     [*] Memory");
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

    // start memory reclaim worker
    tokio::spawn(memory::reclaim_worker());

    timeline.begin("Done!");

    println!(
        "  -  Total boot time: {}ms",
        boot_start.elapsed().as_millis()
    );

    Ok(())
}
