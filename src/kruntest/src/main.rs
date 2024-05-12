use std::process::exit;

use krun::{ConsoleSpec, Machine, VzSpec, MACHINE_STATE_STOPPED};

#[no_mangle]
pub extern "C" fn rsvm_go_on_state_change(state: u32) {
    if state == MACHINE_STATE_STOPPED {
        exit(0);
    }
}

#[no_mangle]
pub extern "C" fn rsvm_go_on_fs_activity() {}

fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();

    let home_dir = std::env::var("HOME").unwrap();

    let mut machine = Machine::new(&VzSpec {
        cpus: 3,
        // 1 GiB
        memory: 1024 * 1024 * 1024,
        kernel: home_dir.clone() + "/kernel",
        #[cfg(target_arch = "x86_64")]
        cmdline: "clocksource=tsc tsc=reliable earlycon=uart,io,0x3f8 console=hvc0 apic=verbose ro root=/dev/vda init=/bin/sh"
            .to_string(),
        #[cfg(target_arch = "aarch64")]
        cmdline: "".to_string(),
        initrd: None,
        console: Some(ConsoleSpec {
            read_fd: 0,
            write_fd: 1,
        }),
        mtu: 1500,
        mac_address_prefix: "00:00:00:00:00".to_string(),
        network_nat: false,
        network_fds: Vec::new(),
        rng: true,
        disk_rootfs: Some(home_dir + "/alpine.img"),
        disk_data: None,
        disk_swap: None,
        balloon: true,
        vsock: false,
        virtiofs: true,
        rosetta: false,
        sound: false,

        nfs_info: None,
    })
    .unwrap();
    machine.start().unwrap();

    // sleep forever
    loop {
        std::thread::sleep(std::time::Duration::from_secs(10000));
    }
}
