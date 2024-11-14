use std::{fs, os::fd::RawFd, path::Path};

use libc::MS_PRIVATE;
use nix::{
    errno::Errno,
    mount::{mount, umount2, MntFlags, MsFlags},
};
use tracing::trace;
use wormhole::newmount::{mount_setattr, MountAttr};

pub const ROOTFS: &str = "/wormhole-rootfs";
pub const UPPERDIR: &str = "/data/upper";
pub const WORKDIR: &str = "/data/work";
pub const WORMHOLE_OVERLAY: &str = "/mnt/wormhole-overlay";
pub const WORMHOLE_UNIFIED: &str = "/mnt/wormhole-unified";
pub const NIX_RW_DIRS: [&str; 3] = ["store", "var", "orb/data"];

pub fn unmount_wormhole() -> anyhow::Result<()> {
    for nix_dir in NIX_RW_DIRS {
        let path = format!("{}/nix/{}", WORMHOLE_UNIFIED, nix_dir);
        trace!("unmounting {}", path);
        match umount2(path.as_str(), MntFlags::MNT_DETACH) {
            Ok(_) => {}
            // ignore EINVAL, which happens if delete_nix_dir already unmounted the submount
            Err(Errno::EINVAL) => {}
            Err(err) => trace!("could not unmount {:?}", err),
        };
    }
    trace!("unmounting {}", WORMHOLE_UNIFIED);
    umount2(WORMHOLE_UNIFIED, MntFlags::empty())?;
    Ok(())
}

pub fn mount_wormhole() -> anyhow::Result<()> {
    // create upper, work, and overlay if they do not exist
    fs::create_dir_all(UPPERDIR)?;
    fs::create_dir_all(WORKDIR)?;
    fs::create_dir_all(WORMHOLE_OVERLAY)?;
    fs::create_dir_all(WORMHOLE_UNIFIED)?;
    fs::create_dir_all("/data/run")?;

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
    // note: to get a ro mount we need to first do a bind mount and then remount ro
    mount(
        Some(ROOTFS),
        WORMHOLE_UNIFIED,
        None::<&Path>,
        MsFlags::MS_BIND,
        None::<&Path>,
    )?;
    mount(
        Some(ROOTFS),
        WORMHOLE_UNIFIED,
        None::<&Path>,
        MsFlags::MS_BIND | MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY,
        None::<&Path>,
    )?;

    // copy over the initial wormhole-rootfs nix store containing base packges into .base
    trace!("creating ro nix store mount");
    mount(
        Some(format!("{}/nix/store", ROOTFS).as_str()),
        format!("{}/nix/orb/sys/.base", WORMHOLE_UNIFIED).as_str(),
        None::<&Path>,
        MsFlags::MS_BIND,
        None::<&Path>,
    )?;
    mount(
        Some(format!("{}/nix/store", ROOTFS).as_str()),
        format!("{}/nix/orb/sys/.base", WORMHOLE_UNIFIED).as_str(),
        None::<&Path>,
        MsFlags::MS_BIND | MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY,
        None::<&Path>,
    )?;

    for nix_dir in NIX_RW_DIRS {
        trace!("mount bind from overlay to unified: {}", nix_dir);
        mount(
            Some(format!("{}/nix/{}", WORMHOLE_OVERLAY, nix_dir).as_str()),
            format!("{}/nix/{}", WORMHOLE_UNIFIED, nix_dir).as_str(),
            None::<&Path>,
            MsFlags::MS_BIND,
            None::<&Path>,
        )?;
    }

    mount_setattr(
        None,
        WORMHOLE_UNIFIED,
        libc::AT_RECURSIVE as u32,
        &MountAttr {
            attr_set: 0,
            attr_clr: 0,
            propagation: MS_PRIVATE,
            userns_fd: 0,
        },
    )?;
    Ok(())
}
