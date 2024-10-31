use std::{fs, path::Path};

use nix::{
    errno::Errno,
    mount::{mount, umount2, MntFlags, MsFlags},
    unistd::ROOT,
};
use tracing::trace;

pub const ROOTFS: &str = "/wormhole-rootfs";
pub const UPPERDIR: &str = "/data/upper";
pub const WORKDIR: &str = "/data/work";
pub const WORMHOLE_OVERLAY: &str = "/mnt/wormhole-overlay";
pub const WORMHOLE_UNIFIED: &str = "/mnt/wormhole-unified";
pub const NIX_RW_DIRS: [&str; 3] = ["store", "var", "orb/data"];

pub fn unmount_wormhole() -> anyhow::Result<()> {
    for path in [
        "/mnt/wormhole-unified/nix/store",
        "/mnt/wormhole-unified/nix/var",
        "/mnt/wormhole-unified/nix/orb/data",
    ] {
        match umount2(path, MntFlags::MNT_DETACH) {
            Ok(_) => {}
            // ignore EINVAL, which happens if delete_nix_dir already unmounted the submount
            Err(Errno::EINVAL) => {}
            Err(err) => trace!("could not unmount {:?}", err),
        };
    }

    umount2("/mnt/wormhole-unified", MntFlags::empty())?;
    Ok(())
}

pub fn mount_wormhole() -> anyhow::Result<()> {
    // create upper, work, and overlay if they do not exist
    fs::create_dir_all(UPPERDIR)?;
    fs::create_dir_all(WORKDIR)?;
    fs::create_dir_all(WORMHOLE_OVERLAY)?;

    trace!("mounting overlayfs");
    let options = format!(
        "lowerdir={},upperdir={},workdir={}",
        ROOTFS, UPPERDIR, WORKDIR
    );
    mount(
        Some("overlay"),
        WORMHOLE_OVERLAY,
        Some("overlay"),
        MsFlags::empty(),
        Some(options.as_str()),
    )?;

    trace!("creating ro wormhole-unified mount");
    mount::<str, str, Path, Path>(Some(ROOTFS), WORMHOLE_UNIFIED, None, MsFlags::MS_BIND, None)?;
    mount::<str, str, Path, Path>(
        Some(ROOTFS),
        WORMHOLE_UNIFIED,
        None,
        MsFlags::MS_BIND | MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY,
        None,
    )?;

    // copy over the initial wormhole-rootfs nix store containing base packges into .base
    trace!("creating ro nix store mount");
    mount::<str, str, Path, Path>(
        Some(format!("{}/nix/store", ROOTFS).as_str()),
        format!("{}/nix/orb/sys/.base", WORMHOLE_UNIFIED).as_str(),
        None,
        MsFlags::MS_BIND,
        None,
    )?;
    mount::<str, str, Path, Path>(
        Some(format!("{}/nix/store", ROOTFS).as_str()),
        format!("{}/nix/orb/sys/.base", WORMHOLE_UNIFIED).as_str(),
        None,
        MsFlags::MS_BIND | MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY,
        None,
    )?;

    for nix_dir in NIX_RW_DIRS {
        trace!("mount bind from overlay to unified: {}", nix_dir);
        mount::<str, str, Path, Path>(
            Some(format!("{}/nix/{}", WORMHOLE_OVERLAY, nix_dir).as_str()),
            format!("{}/nix/{}", WORMHOLE_UNIFIED, nix_dir).as_str(),
            None,
            MsFlags::MS_BIND,
            None,
        )?;
    }
    Ok(())
}
