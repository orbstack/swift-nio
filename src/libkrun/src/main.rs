use std::fmt;

use crossbeam_channel::unbounded;
use hvf::MemoryMapping;
use log::error;
use polly::event_manager::EventManager;
use vmm::{
    resources::VmResources,
    vmm_config::{
        boot_source::{BootSourceConfig, DEFAULT_KERNEL_CMDLINE},
        fs::FsDeviceConfig,
        kernel_bundle::KernelBundle,
        machine_config::VmConfig,
        vsock::VsockDeviceConfig,
    },
};

const INIT_PATH: &str = "/init.krun";

fn main() -> anyhow::Result<()> {
    // === Configure the VM === //

    let mut vmr = VmResources::default();

    // read kernel
    let mut kernel_bytes = std::fs::read("/Applications/OrbStack.app/Contents/Resources/assets/release/arm64/kernel")?;
    // pad up to page size boundary
    let zeros = vec![0u8; 16384 - (kernel_bytes.len() % 16384)];
    kernel_bytes.extend_from_slice(&zeros);

    // Set the kernel image
    {
        vmr.set_kernel_bundle(KernelBundle {
            host_addr: kernel_bytes.as_ptr() as u64,
            guest_addr: 0x80000000,
            entry_addr: 0x80000000,
            size: kernel_bytes.len(),
        })
        .map_err(to_anyhow_error)?;
    }

    // Set the kernel boot config
    {
        let reserve_str = "0x10000\\$0x18690000".to_string();
        let exec_path = "".to_string();
        let work_dir = "".to_string();
        let rlimits = "".to_string();
        let env = "".to_string();
        let args = "".to_string();

        let boot_source = BootSourceConfig {
            kernel_cmdline_prolog: Some(format!(
                "{DEFAULT_KERNEL_CMDLINE} memmap={reserve_str} init={INIT_PATH} {exec_path} {work_dir} {rlimits} {env}",
            )),
            kernel_cmdline_epilog: Some(format!(" -- {args}")),
        };

        vmr.set_boot_source(boot_source).map_err(to_anyhow_error)?;
    }

    // Configure its allowed resources
    {
        let num_vcpus = 2;
        let mem_size_mib = 1024;

        let vm_config = VmConfig {
            vcpu_count: Some(num_vcpus),
            mem_size_mib: Some(mem_size_mib),
            ht_enabled: Some(false),
            cpu_template: None,
        };

        vmr.set_vm_config(&vm_config).map_err(to_anyhow_error)?;
    }

    // Configure its mounted root
    {
        let fs_id = "/dev/root".to_string();
        let shared_dir = "res/drive".to_string();

        vmr.add_fs_device(FsDeviceConfig {
            fs_id,
            shared_dir,
        })
        .map_err(to_anyhow_error)?;
    }

    // Set its network config
    // TODO: replace with virtio-net or add TSI to macvirt test kernel
    /*
    {
        let vsock_device_config = VsockDeviceConfig {
            vsock_id: "vsock0".to_string(),
            guest_cid: 3,
            host_port_map: None,
            unix_ipc_port_map: None,
        };
        vmr.set_vsock_device(vsock_device_config)
            .map_err(to_anyhow_error)?;
    }
    */

    // === Start the VM === //

    let mut event_manager = EventManager::new().map_err(to_anyhow_error_dbg)?;

    let (sender, receiver) = unbounded();
    let vmm =
        vmm::builder::build_microvm(&vmr, &mut event_manager, None, sender).map_err(to_anyhow_error)?;

    let mapper_vmm = vmm.clone();

    std::thread::spawn(move || loop {
        match receiver.recv() {
            Err(e) => error!("Error in receiver: {:?}", e),
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

    loop {
        event_manager.run().map_err(to_anyhow_error_dbg)?;
    }
}

fn to_anyhow_error<E: fmt::Display>(err: E) -> anyhow::Error {
    anyhow::anyhow!("{err}")
}

fn to_anyhow_error_dbg<E: fmt::Debug>(err: E) -> anyhow::Error {
    anyhow::anyhow!("{err:?}")
}
