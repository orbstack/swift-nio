#[cfg(target_arch = "x86_64")]
use std::arch::x86_64::__cpuid_count;
use std::{
    ffi::{c_char, CStr, CString},
    fmt,
    os::raw::c_void,
    sync::{
        atomic::{AtomicI64, Ordering},
        Arc,
    },
    time::Duration,
};
use utils::Mutex;

use anyhow::{anyhow, Context};
use crossbeam_channel::unbounded;
use devices::virtio::{net::device::VirtioNetBackend, CacheType, FsCallbacks, NfsInfo};
use hvf::MemoryMapping;
use libc::strdup;
use nix::{
    sys::time::TimeValLike,
    time::{clock_gettime, ClockId},
};
use once_cell::sync::Lazy;
use polly::event_manager::EventManager;
use serde::{Deserialize, Serialize};
use tracing::error;
#[cfg(target_arch = "aarch64")]
use vmm::vmm_config::kernel_bundle::KernelBundle;
use vmm::{
    builder::ConsoleFds,
    resources::VmResources,
    vmm_config::{
        block::BlockDeviceConfig, boot_source::BootSourceConfig, fs::FsDeviceConfig,
        machine_config::VmConfig, net::NetworkInterfaceConfig, vsock::VsockDeviceConfig,
    },
    VmmShutdownHandle,
};

use tikv_jemallocator::Jemalloc;

#[global_allocator]
static GLOBAL: Jemalloc = Jemalloc;

#[repr(C)]
pub struct GResultCreate {
    ptr: *mut c_void,
    err: *const c_char,
}

#[repr(C)]
pub struct GResultErr {
    err: *const c_char,
}

#[repr(C)]
pub struct GResultIntErr {
    value: i64,
    err: *const c_char,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ConsoleSpec {
    pub read_fd: i32,
    pub write_fd: i32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VzSpec {
    pub cpus: u8,
    pub memory: usize,
    pub kernel: String,
    pub cmdline: String,
    pub initrd: Option<String>,
    pub console: Option<ConsoleSpec>,
    pub mtu: u16,
    pub mac_address_prefix: String,
    pub network_nat: bool,
    pub network_fds: Vec<i32>,
    pub rng: bool,
    pub disk_rootfs: Option<String>,
    pub disk_data: Option<String>,
    pub disk_swap: Option<String>,
    pub balloon: bool,
    pub vsock: bool,
    pub virtiofs: bool,
    pub rosetta: bool,
    pub sound: bool,

    // for loop prevention
    pub nfs_info: Option<NfsInfo>,
}

// ratelimit Go notifications (and hence timer cancellations) to 100ms for perf
const ACTIVITY_NOTIFIER_INTERVAL_MS: i64 = 100;

// due to HVF limitations, we can't have more than one VM per process, so this simplifies things
static GLOBAL_VM: Lazy<Arc<Mutex<Option<Machine>>>> = Lazy::new(|| Arc::new(Mutex::new(None)));
const VM_PTR: usize = 0xdeadbeef;

fn parse_mac_addr(s: &str) -> anyhow::Result<[u8; 6]> {
    Ok(s.split(':')
        .map(|s| u8::from_str_radix(s, 16))
        .collect::<Result<Vec<u8>, _>>()
        .map(|v| v.try_into().unwrap())?)
}

#[derive(Debug)]
struct GoFsCallbacks {
    last_report_time: AtomicI64,
}

impl FsCallbacks for GoFsCallbacks {
    fn on_activity(&self) {
        let now = clock_gettime(ClockId::CLOCK_MONOTONIC)
            .unwrap()
            .num_milliseconds();
        // race doesn't matter - this is an optimization
        if now - self.last_report_time.load(Ordering::Relaxed) >= ACTIVITY_NOTIFIER_INTERVAL_MS {
            self.last_report_time.store(now, Ordering::Relaxed);
            unsafe { rsvm_go_on_fs_activity() };
        }
    }

    fn send_krpc_events(&self, krpc_buf: &[u8]) {
        unsafe {
            swext_fsevents_cb_krpc_events(krpc_buf.as_ptr(), krpc_buf.len());
        }
    }
}

pub struct Machine {
    vmr: Option<VmResources>,
    vmm_shutdown: Option<VmmShutdownHandle>,
}

impl Machine {
    pub fn new(spec: &VzSpec) -> anyhow::Result<Machine> {
        let mut vmr = VmResources::default();

        // on x86, enable HT/SMT if there's an even number of vCPUs, and host has HT/SMT
        #[cfg(target_arch = "x86_64")]
        let ht_enabled = spec.cpus % 2 == 0 && cpuid_has_ht();
        #[cfg(target_arch = "aarch64")]
        let ht_enabled = false;

        // resources
        vmr.set_vm_config(&VmConfig {
            vcpu_count: Some(spec.cpus),
            mem_size_mib: Some(spec.memory / 1024 / 1024),
            ht_enabled: Some(ht_enabled),
            cpu_template: None,
            #[cfg(target_arch = "aarch64")]
            enable_tso: spec.rosetta,
        })
        .map_err(to_anyhow_error)?;

        // kernel
        let mut kernel_data = std::fs::read(&spec.kernel)?;
        #[cfg(target_arch = "aarch64")]
        {
            // pad up to page size boundary
            let page_size = unsafe { libc::sysconf(libc::_SC_PAGESIZE) as usize };
            kernel_data.resize(
                (kernel_data.len() + page_size - 1) / page_size * page_size,
                0,
            );

            vmr.set_kernel_bundle(KernelBundle {
                load_range: 0..kernel_data.len(),
                data: kernel_data,
                guest_addr: 0x80000000,
                entry_addr: 0x80000000,
            })
            .map_err(to_anyhow_error)?;
        }
        #[cfg(target_arch = "x86_64")]
        vmr.set_kernel_bzimage(kernel_data)
            .map_err(to_anyhow_error)?;

        // cmdline
        vmr.set_boot_source(BootSourceConfig {
            kernel_cmdline_prolog: Some(spec.cmdline.clone()),
            kernel_cmdline_epilog: Some("".to_string()),
        })
        .map_err(to_anyhow_error)?;

        // initrd
        if let Some(_) = &spec.initrd {
            return Err(anyhow!("initrd is not supported"));
        }

        // console
        if let Some(console) = &spec.console {
            vmr.set_console_output(ConsoleFds {
                read_fd: console.read_fd,
                write_fd: console.write_fd,
            });
        }

        // network
        if spec.network_nat {
            return Err(anyhow!("network_nat is not supported"));
        }
        for (i, net_fd) in spec.network_fds.iter().enumerate() {
            let mac_addr = format!("{}:{:02x}", spec.mac_address_prefix, i + 1);

            vmr.add_network_interface(NetworkInterfaceConfig {
                iface_id: format!("eth{}", i),
                backend: VirtioNetBackend::Dgram(*net_fd),
                mac: parse_mac_addr(&mac_addr)?,
                mtu: spec.mtu,
            })
            .map_err(to_anyhow_error)?;
        }

        // rng
        if spec.rng {
            vmr.set_rng_device();
        }

        // disks
        if let Some(disk_rootfs) = &spec.disk_rootfs {
            vmr.add_block_device(BlockDeviceConfig {
                block_id: "vda".to_string(),
                cache_type: CacheType::Writeback,
                disk_image_path: disk_rootfs.clone(),
                is_disk_read_only: true,
                is_disk_root: true,
            })
            .map_err(to_anyhow_error)?;
        }
        if let Some(disk_data) = &spec.disk_data {
            vmr.add_block_device(BlockDeviceConfig {
                block_id: "vdb".to_string(),
                cache_type: CacheType::Writeback,
                disk_image_path: disk_data.clone(),
                is_disk_read_only: false,
                is_disk_root: false,
            })
            .map_err(to_anyhow_error)?;
        }
        if let Some(disk_swap) = &spec.disk_swap {
            vmr.add_block_device(BlockDeviceConfig {
                block_id: "vdc".to_string(),
                cache_type: CacheType::Writeback,
                disk_image_path: disk_swap.clone(),
                is_disk_read_only: false,
                is_disk_root: false,
            })
            .map_err(to_anyhow_error)?;
        }

        // balloon
        if spec.balloon {
            vmr.set_balloon_device();
        }

        // vsock
        if spec.vsock {
            vmr.set_vsock_device(VsockDeviceConfig {
                vsock_id: "vsock0".to_string(),
                guest_cid: 2,
                host_port_map: None,
                unix_ipc_port_map: None,
            })
            .map_err(to_anyhow_error)?;
        }

        // virtiofs
        if spec.virtiofs {
            vmr.add_fs_device(FsDeviceConfig {
                fs_id: "mac".to_string(),
                shared_dir: "/".to_string(),
                nfs_info: spec.nfs_info.clone(),
                activity_notifier: Some(Arc::new(GoFsCallbacks {
                    last_report_time: AtomicI64::new(0),
                })),
            })
            .map_err(to_anyhow_error)?;
        }

        // rosetta
        if spec.rosetta {
            vmr.add_fs_device(FsDeviceConfig {
                fs_id: "rosetta".to_string(),
                shared_dir: "/Library/Apple/usr/libexec/oah/RosettaLinux".to_string(),
                nfs_info: None,
                activity_notifier: None,
            })
            .map_err(to_anyhow_error)?;
        }

        // sound
        if spec.sound {
            return Err(anyhow!("sound is not supported"));
        }

        Ok(Machine {
            vmr: Some(vmr),
            vmm_shutdown: None,
        })
    }

    pub fn start(&mut self) -> anyhow::Result<()> {
        anyhow::ensure!(self.vmm_shutdown.is_none(), "vmm already started");

        let mut event_manager = EventManager::new().map_err(to_anyhow_error_dbg)?;

        let (sender, receiver) = unbounded();
        let vmr = self
            .vmr
            .as_ref()
            .ok_or_else(|| anyhow!("already started"))?;
        let vmm = vmm::builder::build_microvm(vmr, &mut event_manager, None, sender)
            .map_err(to_anyhow_error)?;
        let exit_evt = vmm.lock().unwrap().exit_evt();

        if vmr.gpu_virgl_flags.is_some() {
            let mapper_vmm = vmm.clone();
            std::thread::Builder::new()
                .name("VMM GPU mapper".to_string())
                .spawn(move || loop {
                    match receiver.recv() {
                        Err(e) => {
                            error!("Error in receiver: {:?}", e);
                            break;
                        }
                        Ok(m) => match m {
                            MemoryMapping::AddMapping(s, h, g, l) => {
                                mapper_vmm.lock().unwrap().add_mapping(s, h, g, l)
                            }
                            MemoryMapping::RemoveMapping(s, g, l) => {
                                mapper_vmm.lock().unwrap().remove_mapping(s, g, l)
                            }
                        },
                    }
                })?;
        }

        std::thread::Builder::new()
            .name("VMM main loop".to_string())
            .spawn(move || loop {
                event_manager.run().unwrap();

                if event_manager
                    .last_ready_events()
                    .iter()
                    .any(|ev| ev.fd() == exit_evt)
                {
                    tracing::debug!("VM successfully torn-down.");
                    unsafe { rsvm_go_on_state_change(MACHINE_STATE_STOPPED) };
                    break;
                }
            })?;

        self.vmm_shutdown = Some(vmm.lock().unwrap().shutdown_handle());
        self.vmr = None;
        Ok(())
    }

    pub fn stop(&mut self) -> anyhow::Result<()> {
        self.vmm_shutdown
            .take()
            .context("force stop already requested")?
            .request_shutdown();

        Ok(())
    }
}

fn return_owned_cstr(s: &str) -> *const c_char {
    // important: copy and leak the newly allocated string
    let s = CString::new(s).unwrap();
    // required to make it safe to free from C if rust isn't using system allocator
    unsafe { strdup(s.as_ptr()) }
}

// TODO: Add cfg for this.
fn init_logger_once() {
    use std::sync::Once;

    static INIT: Once = Once::new();

    INIT.call_once(|| {
        tracing_subscriber::fmt::init();

        if let Some(filter) = counter::default_env_filter() {
            std::mem::forget(counter::display_every(filter, Duration::from_millis(1000)));
        }
    });
}

#[no_mangle]
pub extern "C" fn rsvm_set_rinit_data(ptr: *const c_void, size: usize) {
    devices::virtio::fs::rosetta::set_rosetta_data(unsafe {
        std::slice::from_raw_parts(ptr as *const u8, size)
    });
}

#[no_mangle]
pub extern "C" fn rsvm_new_machine(
    go_handle: *mut c_void,
    spec_json: *const c_char,
) -> GResultCreate {
    init_logger_once();

    fn inner(_: *mut c_void, spec_json: *const c_char) -> anyhow::Result<*mut c_void> {
        let spec = unsafe { CStr::from_ptr(spec_json) };
        let spec = spec.to_str()?;
        let spec: VzSpec = serde_json::from_str(spec)?;

        let machine = Machine::new(&spec)?;
        // save to global
        *GLOBAL_VM.lock().unwrap() = Some(machine);

        Ok(VM_PTR as *mut c_void)
    }

    match inner(go_handle, spec_json) {
        Ok(ptr) => GResultCreate {
            ptr,
            err: std::ptr::null(),
        },
        Err(e) => GResultCreate {
            ptr: std::ptr::null_mut(),
            err: return_owned_cstr(&e.to_string()),
        },
    }
}

#[no_mangle]
pub extern "C" fn rsvm_machine_destroy(ptr: *mut c_void) {
    if ptr as usize != VM_PTR {
        return;
    }

    GLOBAL_VM.lock().unwrap().take();
}

#[no_mangle]
pub extern "C" fn rsvm_machine_start(ptr: *mut c_void) -> GResultErr {
    fn inner(ptr: *mut c_void) -> anyhow::Result<()> {
        assert_eq!(ptr as usize, VM_PTR, "invalid pointer");

        let mut option = GLOBAL_VM.lock().unwrap();
        let machine = option.as_mut().unwrap();
        machine.start()?;

        Ok(())
    }

    match inner(ptr) {
        Ok(()) => GResultErr {
            err: std::ptr::null(),
        },
        Err(e) => GResultErr {
            err: return_owned_cstr(&e.to_string()),
        },
    }
}

#[no_mangle]
pub extern "C" fn rsvm_machine_stop(ptr: *mut c_void) -> GResultErr {
    fn inner(ptr: *mut c_void) -> anyhow::Result<()> {
        assert_eq!(ptr as usize, VM_PTR, "invalid pointer");

        let mut option = GLOBAL_VM.lock().unwrap();
        let machine = option.as_mut().unwrap();
        machine.stop()?;

        Ok(())
    }

    match inner(ptr) {
        Ok(()) => GResultErr {
            err: std::ptr::null(),
        },
        Err(e) => GResultErr {
            err: return_owned_cstr(&e.to_string()),
        },
    }
}

pub const MACHINE_STATE_STOPPED: u32 = 0;

extern "C" {
    fn rsvm_go_on_state_change(state: u32);
    fn rsvm_go_on_fs_activity();
    fn swext_fsevents_cb_krpc_events(krpc_buf: *const u8, krpc_buf_len: usize);
}

fn to_anyhow_error<E: fmt::Display>(err: E) -> anyhow::Error {
    anyhow!("{err}")
}

fn to_anyhow_error_dbg<E: fmt::Debug>(err: E) -> anyhow::Error {
    anyhow!("{err:?}")
}

#[cfg(target_arch = "x86_64")]
fn cpuid_has_ht() -> bool {
    // topology cpuid, thread level
    let res = unsafe { __cpuid_count(0xb, 0) };
    let num_logical_processors = res.ebx & 0xff;
    num_logical_processors > 1
}
