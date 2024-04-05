use std::{
    ffi::{c_char, CStr, CString},
    fmt,
    os::raw::c_void,
    sync::{Arc, Mutex},
};

use anyhow::anyhow;
use crossbeam_channel::unbounded;
use devices::virtio::{net::device::VirtioNetBackend, CacheType};
use hvf::MemoryMapping;
use libc::strdup;
use log::error;
use once_cell::sync::Lazy;
use polly::event_manager::EventManager;
use serde::{Deserialize, Serialize};
use vmm::{
    builder::ConsoleFds,
    resources::VmResources,
    vmm_config::{
        block::BlockDeviceConfig, boot_source::BootSourceConfig, fs::FsDeviceConfig,
        kernel_bundle::KernelBundle, machine_config::VmConfig, net::NetworkInterfaceConfig,
        vsock::VsockDeviceConfig,
    },
};

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
struct ConsoleSpec {
    read_fd: i32,
    write_fd: i32,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct VzSpec {
    cpus: u8,
    memory: usize,
    kernel: String,
    cmdline: String,
    initrd: Option<String>,
    console: Option<ConsoleSpec>,
    mtu: u16,
    mac_address_prefix: String,
    network_nat: bool,
    network_fds: Vec<i32>,
    rng: bool,
    disk_rootfs: Option<String>,
    disk_data: Option<String>,
    disk_swap: Option<String>,
    balloon: bool,
    vsock: bool,
    virtiofs: bool,
    rosetta: bool,
    sound: bool,
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

struct Machine {
    vmr: Option<VmResources>,
    // must be kept in memory until start
    kernel_bytes: Option<Vec<u8>>,
}

impl Machine {
    fn new(spec: &VzSpec) -> anyhow::Result<Machine> {
        let mut vmr = VmResources::default();

        // resources
        vmr.set_vm_config(&VmConfig {
            vcpu_count: Some(spec.cpus),
            mem_size_mib: Some(spec.memory / 1024 / 1024),
            ht_enabled: Some(false),
            cpu_template: None,
            #[cfg(target_arch = "aarch64")]
            enable_tso: spec.rosetta,
        })
        .map_err(to_anyhow_error)?;

        // kernel
        let mut kernel_bytes = std::fs::read(&spec.kernel)?;
        // pad up to page size boundary
        kernel_bytes.resize(
            kernel_bytes.len() + (16384 - (kernel_bytes.len() % 16384)),
            0,
        );
        vmr.set_kernel_bundle(KernelBundle {
            host_addr: kernel_bytes.as_ptr() as u64,
            guest_addr: 0x80000000,
            entry_addr: 0x80000000,
            size: kernel_bytes.len(),
        })
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
        if !spec.rng {
            return Err(anyhow!("disabling rng is not supported"));
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
        if !spec.balloon {
            return Err(anyhow!("disabling balloon is not supported"));
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
            })
            .map_err(to_anyhow_error)?;
        }

        // rosetta
        if spec.rosetta {
            // TODO: TSO, ioctl, etc.
            vmr.add_fs_device(FsDeviceConfig {
                fs_id: "rosetta".to_string(),
                shared_dir: "/Library/Apple/usr/libexec/oah/RosettaLinux".to_string(),
            })
            .map_err(to_anyhow_error)?;
        }

        // sound
        if spec.sound {
            return Err(anyhow!("sound is not supported"));
        }

        Ok(Machine {
            vmr: Some(vmr),
            kernel_bytes: Some(kernel_bytes),
        })
    }

    fn start(&mut self) -> anyhow::Result<()> {
        let mut event_manager = EventManager::new().map_err(to_anyhow_error_dbg)?;

        let (sender, receiver) = unbounded();
        let vmr = self
            .vmr
            .as_ref()
            .ok_or_else(|| anyhow!("already started"))?;
        let vmm = vmm::builder::build_microvm(vmr, &mut event_manager, None, sender)
            .map_err(to_anyhow_error)?;
        let exit_evt = vmm.lock().unwrap().get_exit_evt();

        let mapper_vmm = vmm.clone();

        std::thread::spawn(move || loop {
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
        });

        std::thread::spawn(move || loop {
            event_manager.run().unwrap();

            if event_manager
                .last_ready_events()
                .iter()
                .any(|ev| ev.fd() == exit_evt)
            {
                log::info!("VM successfully torn-down.");
                unsafe { rsvm_go_on_state_change(MACHINE_STATE_STOPPED) };
                break;
            }
        });

        // must be retained until copied into guest memory by build_microvm
        self.kernel_bytes.take().unwrap();
        self.vmr.take().unwrap();
        Ok(())
    }

    fn stop(&self) -> anyhow::Result<()> {
        unimplemented!()
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

    INIT.call_once(|| env_logger::init());
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

const MACHINE_STATE_STOPPED: u32 = 0;

extern "C" {
    fn rsvm_go_on_state_change(state: u32);
}

fn to_anyhow_error<E: fmt::Display>(err: E) -> anyhow::Error {
    anyhow!("{err}")
}

fn to_anyhow_error_dbg<E: fmt::Debug>(err: E) -> anyhow::Error {
    anyhow!("{err:?}")
}
