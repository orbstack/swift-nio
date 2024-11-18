use std::{fs, process::exit};

use nix::mount::{umount2, MntFlags, MsFlags};
use tracing::debug;
use wormhole::{bind_mount_ro, mount_common};

pub const ROOTFS: &str = "/wormhole-rootfs";
pub const UPPERDIR: &str = "/data/upper";
pub const WORKDIR: &str = "/data/work";
pub const WORMHOLE_OVERLAY: &str = "/mnt/wormhole-overlay";
pub const WORMHOLE_UNIFIED: &str = "/mnt/wormhole-unified";
pub const NIX_RW_DIRS: [&str; 3] = ["store", "var", "orb/data"];
pub const BUF_SIZE: usize = 65536;

pub fn unmount_wormhole() -> anyhow::Result<()> {
    for nix_dir in NIX_RW_DIRS {
        let path = format!("{}/nix/{}", WORMHOLE_UNIFIED, nix_dir);
        debug!("unmounting {}", path);
        match umount2(path.as_str(), MntFlags::MNT_DETACH) {
            Ok(_) => {}
            Err(err) => debug!("could not unmount {:?}", err),
        };
    }
    debug!("unmounting {}", WORMHOLE_UNIFIED);
    umount2(
        format!("{}/nix/orb/sys/.base", WORMHOLE_UNIFIED).as_str(),
        MntFlags::empty(),
    )?;
    umount2(WORMHOLE_UNIFIED, MntFlags::empty())?;

    Ok(())
}

pub fn mount_wormhole() -> anyhow::Result<()> {
    // create upper, work, and overlay if they do not exist
    fs::create_dir_all(UPPERDIR)?;
    fs::create_dir_all(WORKDIR)?;
    fs::create_dir_all(WORMHOLE_OVERLAY)?;
    fs::create_dir_all(WORMHOLE_UNIFIED)?;
    fs::create_dir_all("/run")?;

    debug!("mounting overlayfs");
    let options = format!(
        "lowerdir={},upperdir={},workdir={}",
        ROOTFS, UPPERDIR, WORKDIR
    );
    mount_common(
        "overlay",
        WORMHOLE_OVERLAY,
        Some("overlay"),
        MsFlags::empty(),
        Some(options.as_str()),
    )?;

    debug!("creating ro wormhole-unified mount");
    // mount a r-o nix to protect /nix/orb/sys and prevent creating files in /nix/.
    bind_mount_ro(ROOTFS, WORMHOLE_UNIFIED)?;
    // copy over the initial wormhole-rootfs nix store containing base packages into .base
    bind_mount_ro(
        format!("{}/nix/store", ROOTFS).as_str(),
        format!("{}/nix/orb/sys/.base", WORMHOLE_UNIFIED).as_str(),
    )?;

    for nix_dir in NIX_RW_DIRS {
        debug!("mount bind from overlay to unified: {}", nix_dir);
        mount_common(
            format!("{}/nix/{}", WORMHOLE_OVERLAY, nix_dir).as_str(),
            format!("{}/nix/{}", WORMHOLE_UNIFIED, nix_dir).as_str(),
            None,
            MsFlags::MS_BIND,
            None,
        )?;
    }

    Ok(())
}

pub fn shutdown() -> anyhow::Result<()> {
    unmount_wormhole()?;
    exit(0);
}
