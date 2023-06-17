use std::{env, error::Error, fs::{self, Permissions}, time::Instant, os::unix::prelude::PermissionsExt};

use nix::{sys::{stat::{umask, Mode}, resource::{setrlimit, Resource}, self, wait::waitpid}, mount::{MsFlags}, unistd, libc::{RLIM_INFINITY, swapon, self}};
use futures_util::TryStreamExt;

mod helpers;
use helpers::{sysctl, SWAP_FLAG_DISCARD, SWAP_FLAG_PREFER, SWAP_FLAG_PRIO_SHIFT, SWAP_FLAG_PRIO_MASK};

mod vcontrol;

// debug flag
static DEBUG: bool = true;

// da:9b:d0:64:e1:01
const VNET_LLADDR: &[u8] = &[0xda, 0x9b, 0xd0, 0x64, 0xe1, 0x01];

struct SystemInfo {
    kernel_version: String,
    cmdline: Vec<String>,
}

fn get_system_info() -> Result<SystemInfo, Box<dyn Error>> {
    // trim newline
    let kernel_version = fs::read_to_string("/proc/sys/kernel/osrelease")?.trim().to_string();
    let cmdline = fs::read_to_string("/proc/cmdline")?.trim()
        .split(' ')
        .map(|s| s.to_string())
        .filter(|s| s.starts_with("orb."))
        .collect();

    Ok(SystemInfo {
        kernel_version,
        cmdline,
    })
}

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
    unistd::sethostname("orbhost")?;

    // rlimit
    setrlimit(Resource::RLIMIT_NOFILE, 1048576, 1048576)?;
    setrlimit(Resource::RLIMIT_MEMLOCK, RLIM_INFINITY, RLIM_INFINITY)?;

    Ok(())
}

fn mount_common(source: &str, dest: &str, fstype: Option<&str>, flags: MsFlags, data: Option<&str>) -> Result<(), Box<dyn Error>> {
    if let Err(e) = nix::mount::mount(Some(source), dest, fstype, flags, data) {
        return Err(format!("Failed to mount {} to {}: {}", source, dest, e).into());
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
    let mut flags = MsFlags::MS_NOATIME;
    if !DEBUG {
        // secure flags in release
        flags |= MsFlags::MS_NODEV | MsFlags::MS_NOSUID | MsFlags::MS_NOEXEC;
    }
    mount("tmpfs", "/run", "tmpfs", flags, None)?;
    // create directories
    fs::create_dir_all("/run/overlay/root")?;
    fs::create_dir_all("/run/overlay/upper")?;
    fs::create_dir_all("/run/overlay/work")?;
    fs::create_dir_all("/run/overlay/merged")?;
    // bind mount root
    bind_mount("/", "/run/overlay/root", None)?;
    // mount overlayfs - with vanity name for "df"
    mount("orbstack", "/run/overlay/merged", "overlay", flags, Some("lowerdir=/run/overlay/root,upperdir=/run/overlay/upper,workdir=/run/overlay/work"))?;

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
        cd $1
        mount --move . /
        chroot .
        exec /sbin/init "$@"
    */
    env::set_current_dir("/run/overlay/merged")?;
    mount_common(".", "/", None, MsFlags::MS_MOVE, None)?;
    unistd::chroot(".")?;
    env::set_current_dir("/")?; // TOOD needed?

    Ok(())
}

fn mount_pseudo_fs() -> Result<(), Box<dyn Error>> {
    let secure_flags = MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID | MsFlags::MS_NODEV | MsFlags::MS_RELATIME;
    let dev_flags = MsFlags::MS_NOEXEC | MsFlags::MS_NOSUID | MsFlags::MS_RELATIME;

    // essential
    mount("proc", "/proc", "proc", secure_flags, None)?;
    mount("sysfs", "/sys", "sysfs", secure_flags, None)?;
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

fn apply_system_settings() -> Result<(), Box<dyn Error>> {
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

async fn add_vnet_neighbor(ip_neigh: &rtnetlink::NeighbourHandle, eth0_index: u32, ip_addr: &str) -> Result<(), Box<dyn Error>> {
    ip_neigh.add(eth0_index, ip_addr.parse()?)
        .link_local_address(VNET_LLADDR)
        .execute().await?;
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
    tokio::spawn(conn);
    let mut ip_link = handle.link();
    let ip_addr = handle.address();
    let ip_route = handle.route();
    let ip_neigh = handle.neighbours();

    // loopback: set lo up
    let lo = ip_link.get().match_name("lo".into()).execute().try_next().await?.unwrap();
    ip_link.set(lo.header.index).up().execute().await?;

    // main gvisor NAT network
    let eth0 = ip_link.get().match_name("eth0".into()).execute().try_next().await?.unwrap();
    let eth0_index = eth0.header.index;

    // static neighbors to reduce ARP CPU usage
    add_vnet_neighbor(&ip_neigh, eth0_index, "198.19.248.1").await?;
    add_vnet_neighbor(&ip_neigh, eth0_index, "198.19.248.200").await?;
    add_vnet_neighbor(&ip_neigh, eth0_index, "198.19.248.201").await?;
    add_vnet_neighbor(&ip_neigh, eth0_index, "198.19.248.253").await?;
    add_vnet_neighbor(&ip_neigh, eth0_index, "198.19.248.254").await?;
    // only one IPv6: others are on ext subnet (to avoid NDP)
    add_vnet_neighbor(&ip_neigh, eth0_index, "fd07:b51a:cc66:00f0::1").await?;

    // set eth0 mtu, up
    ip_link.set(eth0_index)
        .mtu(1500)
        .up()
        .execute().await?;

    // add IP addresses
    ip_addr.add(eth0_index, "198.19.248.2".parse()?, 24).execute().await?;
    ip_addr.add(eth0_index, "fd07:b51a:cc66:00f0::2".parse()?, 64).execute().await?;

    // add default routes
    ip_route.add().v4().gateway("198.19.248.1".parse()?).execute().await?;
    ip_route.add().v6().gateway("fd07:b51a:cc66:00f0::1".parse()?).execute().await?;

    // scon machine bridge: eth1 mtu, up
    // scon deals with the rest
    // cannot use static neigh because macOS generates MAC addr
    let eth1 = ip_link.get().match_name("eth1".into()).execute().try_next().await?.unwrap();
    ip_link.set(eth1.header.index)
        .mtu(1500)
        .up()
        .execute().await?;

    // docker vlan router
    // scon deals with the rest
    let eth2 = ip_link.get().match_name("eth2".into()).execute().try_next().await?.unwrap();
    ip_link.set(eth2.header.index)
        .mtu(1500)
        .up()
        .execute().await?;

    Ok(())
}

fn mount_data() -> Result<(), Box<dyn Error>> {
    // virtiofs share
    mount("mac", "/mnt/mac", "virtiofs", MsFlags::MS_RELATIME, None)?;

    // data
    // first try with regular mount, then try usebackuproot
    let data_flags = MsFlags::MS_NOATIME;
    // TODO: fix duplicate flags
    if let Err(e) = mount("/dev/vdb1", "/data", "btrfs", data_flags, Some("discard=async,space_cache=v2,ssd,nodatacow,nodatasum,quota_statfs")) {
        println!(" !!! Failed to mount data: {}", e);
        println!(" [*] Attempting to recover data");
        mount("/dev/vdb1", "/data", "btrfs", data_flags, Some("discard=async,space_cache=v2,ssd,nodatacow,nodatasum,quota_statfs,usebackuproot"))?;
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
    if let Ok(_) = mount("rosetta", "/mnt/rosetta", "virtiofs", MsFlags::empty(), None) {
        // rosetta
        println!("  ?  Using Rosetta");

        // TODO: add preserve-argv0 flag on Sonoma
        let rosetta_flags = "CF@";
        add_binfmt("rosetta", r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00"#, Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[rosetta]", rosetta_flags)?;

        // Buildkit Rosetta amd64 stub workaround
        // register after rosetta to rank before it in binfmt_misc entries list (since rosetta matches this too)
        // first 256 bytes = BINPRM_BUF_SIZE
        // big, must be in one write to binfmt_misc
        add_binfmt("bk-stub-amd64", r#"\x7f\x45\x4c\x46\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00\x01\x00\x00\x00\x00\x10\x40\x00\x00\x00\x00\x00\x40\x00\x00\x00\x00\x00\x00\x00\xe0\x11\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x40\x00\x38\x00\x03\x00\x40\x00\x06\x00\x05\x00\x01\x00\x00\x00\x04\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x40\x00\x00\x00\x00\x00\x00\x00\x40\x00\x00\x00\x00\x00\x0c\x01\x00\x00\x00\x00\x00\x00\x0c\x01\x00\x00\x00\x00\x00\x00\x00\x10\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x05\x00\x00\x00\x00\x10\x00\x00\x00\x00\x00\x00\x00\x10\x40\x00\x00\x00\x00\x00\x00\x10\x40\x00\x00\x00\x00\x00\x70\x00\x00\x00\x00\x00\x00\x00\x70\x00\x00\x00\x00\x00\x00\x00\x00\x10\x00\x00\x00\x00\x00\x00\x04\x00\x00\x00\x04\x00\x00\x00\xe8\x00\x00\x00\x00\x00\x00\x00\xe8\x00\x40\x00\x00\x00\x00\x00\xe8\x00\x40\x00\x00\x00\x00\x00\x24\x00\x00\x00\x00\x00\x00\x00\x24\x00\x00\x00\x00\x00\x00\x00\x04\x00\x00\x00\x00\x00\x00\x00\x04\x00\x00\x00\x14\x00\x00\x00\x03\x00\x00\x00\x47\x4e\x55\x00\x65\xa5\xb7\x39\xd6\xa8\xe5\x56"#, None, "[rosetta-bk-stub]", "CF")?;
    } else {
        // qemu
        println!("  ?  Using QEMU");

        add_binfmt("qemu-x86_64", r#"\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00"#, Some(r#"\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff"#), "[qemu]", "POCF")?;
    }

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
    std::process::Command::new("/sbin/mkswap")
        .arg("/dev/zram0")
        .output()?;
    // enable
    enable_swap("/dev/zram0", 32767)?;

    // emergency disk swap (1 GiB)
    enable_swap("/dev/vdc2", 1)?;

    Ok(())
}

fn start_services(sys_info: &SystemInfo) -> Result<(), Box<dyn Error>> {
    // chronyd
    let chrony_process = std::process::Command::new("/usr/sbin/chronyd")
        .arg("-d") // foreground, log to stderr
        .arg("-f") // config file
        .arg("/etc/chrony/chrony.conf")
        .spawn()?;
    // set OOM score adj for critical service
    let chrony_pid = chrony_process.id();
    fs::write(format!("/proc/{}/oom_score_adj", chrony_pid), "-950")?;

    // scon
    let scon_process = std::process::Command::new("/opt/orb/scon")
        .arg("mgr")
        .args(&sys_info.cmdline) // pass cmdline args for console
        .spawn()?;
    // set OOM score adj for critical service
    let scon_pid = scon_process.id();
    fs::write(format!("/proc/{}/oom_score_adj", scon_pid), "-950")?;

    // sshd
    if DEBUG {
        std::process::Command::new("/usr/sbin/sshd")
            .arg("-D") // foreground
            .arg("-e") // log to stderr
            .spawn()?;
    }

    Ok(())
}

struct BootTracker {
    last_stage_start: Instant,
}

impl BootTracker {
    fn new() -> BootTracker {
        BootTracker {
            last_stage_start: Instant::now(),
        }
    }

    fn begin(&mut self, stage: &str) {
        let now = Instant::now();
        let diff = now.duration_since(self.last_stage_start);
        println!(" [*] {}  (+{}ms)", stage, diff.as_millis());
        self.last_stage_start = now;
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let mut tracker = BootTracker::new();
    let boot_start = Instant::now();

    tracker.begin("Booting OrbStack");

    // set basic environment
    tracker.begin("Setting basic environment");
    set_basic_env()?;

    // pivot to overlayfs
    tracker.begin("Pivoting to overlayfs");
    setup_overlayfs()?;

    // mount basic filesystems
    tracker.begin("Mounting pseudo filesystems");
    mount_pseudo_fs()?;

    // system info
    // only works after pseudo-fs mounted
    let sys_info = get_system_info()?;
    println!("  ?  Kernel version: {}", sys_info.kernel_version);
    println!("  ?  Command line: {}", sys_info.cmdline.join(" "));
    println!();

    tracker.begin("Setting up binfmt");
    setup_binfmt(&sys_info)?;

    // TODO: start udev/smdev

    tracker.begin("Setting up network");
    setup_network().await?;

    tracker.begin("Starting control server");
    tokio::spawn(vcontrol::server_main());

    // TODO: resize data partition

    // do the following 3 slow stages in parallel
    // speedup: 300-400 ms -> 250 ms
    tracker.begin("Late tasks");
    let mut tasks = vec![];
    tasks.push(std::thread::spawn(|| { // 150 ms
        //let stage_start = Instant::now();
        println!("     [*] Applying system settings");
        apply_system_settings().unwrap();
        //println!("     ... Applying system settings: +{}ms", stage_start.elapsed().as_millis());
    }));
    tasks.push(std::thread::spawn(|| { // 50 ms
        //let stage_start = Instant::now();
        println!("     [*] Mounting data");
        mount_data().unwrap();
        //println!("     ... Mounting data: +{}ms", stage_start.elapsed().as_millis());
    }));
    tasks.push(std::thread::spawn(|| { // 70 ms
        //let stage_start = Instant::now();
        println!("     [*] Setting up memory");
        setup_memory().unwrap();
        //println!("     ... Setting up memory: +{}ms", stage_start.elapsed().as_millis());
    }));
    for task in tasks {
        task.join().unwrap();
    }

    tracker.begin("Initializing data");
    init_data()?;

    tracker.begin("Starting services");
    start_services(&sys_info)?;

    tracker.begin("Booted!");

    println!(" ?  Total boot time: {}ms", boot_start.elapsed().as_millis());

    // reap children
    loop {
        let res = waitpid(None, None);
        println!(" !!! Reaped child: {:?}", res);
    }
}
