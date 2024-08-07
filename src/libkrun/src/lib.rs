#[cfg(target_arch = "x86_64")]
use std::arch::x86_64::__cpuid_count;
use std::{
    ffi::{c_char, CStr, CString},
    fmt,
    os::{fd::RawFd, raw::c_void},
    sync::Arc,
    time::Duration,
};
use tracing_subscriber::{fmt::format::FmtSpan, EnvFilter};
use utils::Mutex;

use anyhow::{anyhow, Context};
use crossbeam_channel::unbounded;
use devices::virtio::{
    net::device::VirtioNetBackend, port_io::dup_raw_fd_into_owned, CacheType, FsCallbacks, NfsInfo,
};
#[cfg(target_arch = "x86_64")]
use hvf::check_cpuid;
use hvf::{profiler::ProfilerParams, HvfVm, MemoryMapping};
use libc::strdup;
use once_cell::sync::Lazy;
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
    Vmm, VmmShutdownHandle,
};

#[repr(C)]
pub struct GResultCreate {
    ptr: *mut c_void,
    err: *const c_char,
}

impl GResultCreate {
    pub fn from_result(r: anyhow::Result<*mut c_void>) -> GResultCreate {
        match r {
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
}

#[repr(C)]
pub struct GResultErr {
    err: *const c_char,
}

impl GResultErr {
    pub fn from_result<T>(r: Result<T, anyhow::Error>) -> GResultErr {
        match r {
            Ok(_) => GResultErr {
                err: std::ptr::null(),
            },
            Err(e) => GResultErr {
                err: return_owned_cstr(&e.to_string()),
            },
        }
    }
}

#[repr(C)]
pub struct GResultIntErr {
    value: i64,
    err: *const c_char,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ConsoleSpec {
    pub read_fd: RawFd,
    pub write_fd: RawFd,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct VzSpec {
    pub cpus: u8,
    pub memory: usize,
    pub kernel: String,
    pub kernel_csmap: Option<String>,
    pub cmdline: String,
    pub initrd: Option<String>,
    pub console: Option<ConsoleSpec>,
    pub mtu: u16,
    pub mac_address_prefix: String,
    pub network_nat: bool,
    pub network_fds: Vec<RawFd>,
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

// due to HVF limitations, we can't have more than one VM per process, so this simplifies things
static GLOBAL_VM: Lazy<Arc<Mutex<Option<Machine>>>> = Lazy::new(|| Arc::new(Mutex::new(None)));
const VM_PTR: usize = 0xdeadbeef;

fn parse_mac_addr(s: &str) -> anyhow::Result<[u8; 6]> {
    Ok(s.split(':')
        .map(|s| u8::from_str_radix(s, 16))
        .collect::<Result<Vec<u8>, _>>()
        .map(|v| v.try_into().unwrap())?)
}

// same as Go mem.PhysicalMemory
fn system_total_memory() -> usize {
    let pages = unsafe { libc::sysconf(libc::_SC_PHYS_PAGES) };
    let page_size = unsafe { libc::sysconf(libc::_SC_PAGE_SIZE) };
    pages as usize * page_size as usize
}

#[derive(Debug)]
struct GoFsCallbacks {}

impl FsCallbacks for GoFsCallbacks {
    fn send_krpc_events(&self, krpc_buf: &[u8]) {
        unsafe {
            swext_fsevents_cb_krpc_events(krpc_buf.as_ptr(), krpc_buf.len());
        }
    }
}

pub struct Machine {
    vmr: Option<VmResources>,
    vmm: Option<Arc<Mutex<Vmm>>>,
    vmm_shutdown: Option<VmmShutdownHandle>,
}

impl Machine {
    pub fn new(spec: &VzSpec) -> anyhow::Result<Machine> {
        let mut vmr = VmResources::default();

        // on x86, check CPU compatibility early to return a better error
        #[cfg(target_arch = "x86_64")]
        check_cpuid()?;

        // on x86, enable HT/SMT if there's an even number of vCPUs, and host has HT/SMT
        #[cfg(target_arch = "x86_64")]
        let ht_enabled = spec.cpus % 2 == 0 && cpuid_has_ht();
        #[cfg(target_arch = "aarch64")]
        let ht_enabled = false;

        // clamp memory
        let mem_size = spec
            .memory
            .min(HvfVm::max_ram_size()? as usize)
            .min(system_total_memory());

        // resources
        vmr.set_vm_config(&VmConfig {
            vcpu_count: Some(spec.cpus),
            mem_size_mib: Some(mem_size / 1024 / 1024),
            ht_enabled: Some(ht_enabled),
            cpu_template: None,
            #[cfg(target_arch = "aarch64")]
            enable_tso: spec.rosetta,
        })
        .map_err(to_anyhow_error)?;

        // kernel
        let kernel_data = std::fs::read(&spec.kernel).map_err(|e| anyhow!("read kernel: {}", e))?;
        #[cfg(target_arch = "aarch64")]
        {
            vmr.set_kernel_bundle(KernelBundle {
                load_range: 0..kernel_data.len(),
                data: kernel_data,
                guest_addr: arch::aarch64::get_kernel_start(),
                entry_addr: arch::aarch64::get_kernel_start(),
                csmap_path: spec.kernel_csmap.clone(),
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
        if spec.initrd.is_some() {
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
        for (i, &net_fd) in spec.network_fds.iter().enumerate() {
            let mac_addr = format!("{}:{:02x}", spec.mac_address_prefix, i + 1);

            // make an owned copy of the fd
            let owned_fd = Arc::new(dup_raw_fd_into_owned(net_fd)?);
            vmr.add_network_interface(NetworkInterfaceConfig {
                iface_id: format!("eth{}", i),
                backend: VirtioNetBackend::Dgram(owned_fd),
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
            vmr.add_block_device(
                vmr.vcpu_config().vcpu_count,
                BlockDeviceConfig {
                    block_id: "vda".to_string(),
                    cache_type: CacheType::Writeback,
                    disk_image_path: disk_rootfs.clone(),
                    is_disk_read_only: true,
                    is_disk_root: true,
                },
            )
            .map_err(to_anyhow_error)?;
        }
        if let Some(disk_data) = &spec.disk_data {
            vmr.add_block_device(
                vmr.vcpu_config().vcpu_count,
                BlockDeviceConfig {
                    block_id: "vdb".to_string(),
                    cache_type: CacheType::Writeback,
                    disk_image_path: disk_data.clone(),
                    is_disk_read_only: false,
                    is_disk_root: false,
                },
            )
            .map_err(to_anyhow_error)?;
        }
        if let Some(disk_swap) = &spec.disk_swap {
            vmr.add_block_device(
                vmr.vcpu_config().vcpu_count,
                BlockDeviceConfig {
                    block_id: "vdc".to_string(),
                    cache_type: CacheType::Writeback,
                    disk_image_path: disk_swap.clone(),
                    is_disk_read_only: false,
                    is_disk_root: false,
                },
            )
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
                activity_notifier: Some(Arc::new(GoFsCallbacks {})),
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
            vmm: None,
            vmm_shutdown: None,
        })
    }

    pub fn start(&mut self) -> anyhow::Result<()> {
        anyhow::ensure!(self.vmm_shutdown.is_none(), "vmm already started");

        let mut event_manager = gruel::EventManager::new().map_err(to_anyhow_error_dbg)?;

        let (sender, receiver) = unbounded();
        let vmr = self
            .vmr
            .as_ref()
            .ok_or_else(|| anyhow!("already started"))?;
        let vmm = vmm::builder::build_microvm(vmr, &mut event_manager, None, sender)
            .map_err(to_anyhow_error)?;

        if vmr.gpu_virgl_flags.is_some() {
            let vmm = vmm.clone();
            std::thread::Builder::new()
                .name("VMM GPU mapper".to_string())
                .spawn(move || loop {
                    match receiver.recv() {
                        Err(e) => {
                            error!("Error in receiver: {:?}", e);
                            break;
                        }
                        Ok(m) => match m {
                            MemoryMapping::AddMapping(s, h, g, l) => unsafe {
                                vmm.lock().unwrap().add_mapping(s, h as *mut u8, g, l)
                            },
                            MemoryMapping::RemoveMapping(s, g, l) => {
                                vmm.lock().unwrap().remove_mapping(s, g, l)
                            }
                        },
                    }
                })?;
        }

        std::thread::Builder::new()
            .name("VMM main loop".to_string())
            .spawn(move || {
                let counter_display = counter::default_env_filter()
                    .map(|filter| counter::display_every(filter, Duration::from_millis(1000)));

                event_manager.run();

                drop(counter_display);
                tracing::info!("VM stopped");
                unsafe { rsvm_go_on_state_change(MACHINE_STATE_STOPPED) };
            })?;

        self.vmm_shutdown = Some(vmm.lock().unwrap().shutdown_handle());
        self.vmm = Some(vmm);
        self.vmr = None;
        Ok(())
    }

    pub fn dump_debug(&self) -> anyhow::Result<()> {
        let vmm = self
            .vmm
            .as_ref()
            .ok_or_else(|| anyhow!("not started"))?
            .lock()
            .unwrap();
        vmm.dump_debug();
        Ok(())
    }

    pub fn stop(&mut self) -> anyhow::Result<()> {
        self.vmm_shutdown
            .take()
            .context("force stop already requested")?
            .request_shutdown();

        Ok(())
    }

    pub fn start_profile(&self, params: &ProfilerParams) -> anyhow::Result<()> {
        let mut vmm = self
            .vmm
            .as_ref()
            .ok_or_else(|| anyhow!("not started"))?
            .lock()
            .unwrap();
        vmm.start_profile(params)?;
        Ok(())
    }

    pub fn stop_profile(&self) -> anyhow::Result<()> {
        let mut vmm = self
            .vmm
            .as_ref()
            .ok_or_else(|| anyhow!("not started"))?
            .lock()
            .unwrap();
        vmm.stop_profile()?;
        Ok(())
    }

    fn with<T>(ptr: *mut c_void, f: impl FnOnce(&mut Machine) -> T) -> T {
        assert_eq!(ptr as usize, VM_PTR, "invalid pointer");

        let mut option = GLOBAL_VM.lock().unwrap();
        let machine = option.as_mut().unwrap();
        f(machine)
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
        tracing_subscriber::fmt::fmt()
            .with_env_filter(EnvFilter::from_default_env())
            .with_span_events(FmtSpan::CLOSE)
            .init();
    });
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_set_rinit_data(ptr: *const u8, size: usize) {
    devices::virtio::fs::rosetta::set_rosetta_data(unsafe {
        std::slice::from_raw_parts(ptr, size)
    });
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_new_machine(
    go_handle: *mut c_void,
    spec_json: *const c_char,
) -> GResultCreate {
    init_logger_once();

    GResultCreate::from_result((|| {
        let spec = unsafe { CStr::from_ptr(spec_json) };
        let spec: VzSpec = serde_json::from_str(spec.to_str()?)?;

        let machine = Machine::new(&spec)?;
        // save to global
        *GLOBAL_VM.lock().unwrap() = Some(machine);

        Ok(VM_PTR as *mut c_void)
    })())
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_machine_destroy(ptr: *mut c_void) {
    if ptr as usize != VM_PTR {
        return;
    }

    GLOBAL_VM.lock().unwrap().take();
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_machine_start(ptr: *mut c_void) -> GResultErr {
    GResultErr::from_result(Machine::with(ptr, |machine| machine.start()))
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_machine_dump_debug(ptr: *mut c_void) -> GResultErr {
    GResultErr::from_result(Machine::with(ptr, |machine| machine.dump_debug()))
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_machine_start_profile(
    ptr: *mut c_void,
    params: *const u8,
    params_len: usize,
) -> GResultErr {
    GResultErr::from_result(Machine::with(ptr, |machine| {
        let params = std::slice::from_raw_parts(params, params_len);
        let params: ProfilerParams = serde_json::from_slice(params)?;
        machine.start_profile(&params)
    }))
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_machine_stop_profile(ptr: *mut c_void) -> GResultErr {
    GResultErr::from_result(Machine::with(ptr, |machine| machine.stop_profile()))
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_machine_stop(ptr: *mut c_void) -> GResultErr {
    GResultErr::from_result(Machine::with(ptr, |machine| machine.stop()))
}

pub const MACHINE_STATE_STOPPED: u32 = 0;

extern "C" {
    fn rsvm_go_on_state_change(state: u32);
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
