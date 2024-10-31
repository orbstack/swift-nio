use anyhow::Context;
use tracing::trace;
use wormhole::newmount::{mount_setattr, MountAttr, MOUNT_ATTR_RDONLY};

pub fn with_remount_rw<T>(mut f: impl FnMut() -> std::io::Result<T>) -> anyhow::Result<T> {
    match f() {
        Ok(res) => Ok(res),
        Err(ref e) if e.raw_os_error() == Some(libc::EROFS) => {
            // remount RW
            trace!("remount / as RW");
            mount_setattr(
                None,
                "/",
                0,
                &MountAttr {
                    attr_set: 0,
                    attr_clr: MOUNT_ATTR_RDONLY,
                    propagation: 0,
                    userns_fd: 0,
                },
            )
            .context("remount RW")?;

            let res = f();

            // remount RO
            trace!("remount / as RO");
            mount_setattr(
                None,
                "/",
                0,
                &MountAttr {
                    attr_set: 0,
                    attr_clr: MOUNT_ATTR_RDONLY,
                    propagation: 0,
                    userns_fd: 0,
                },
            )
            .context("remount RO")?;

            Ok(res?)
        }
        Err(e) => Err(e.into()),
    }
}
