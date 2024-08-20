use anyhow::anyhow;
use nix::errno::Errno;

extern "C" {
    fn setiopolicy_np(iotype: libc::c_int, scope: libc::c_int, policy: libc::c_int) -> libc::c_int;
}

const IOPOL_TYPE_VFS_ATIME_UPDATES: libc::c_int = 2;
const IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES: libc::c_int = 3;

const IOPOL_SCOPE_THREAD: libc::c_int = 1;

const IOPOL_ATIME_UPDATES_OFF: libc::c_int = 1;
const IOPOL_MATERIALIZE_DATALESS_FILES_OFF: libc::c_int = 1;

pub fn prepare_vcpu_for_hvc() -> anyhow::Result<()> {
    // don't allow materializing dataless files:
    // it can block for ~1 sec, which is bad for vCPU thread
    // Apple only started using dataless files for FileProvider in macOS 14 Sonoma (officially announced for the first time at WWDC 2023), but the kernel has supported them since at least macOS 12 (XNU 8020.x) so this doesn't need a version check
    let ret = unsafe {
        setiopolicy_np(
            IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES,
            IOPOL_SCOPE_THREAD,
            IOPOL_MATERIALIZE_DATALESS_FILES_OFF,
        )
    };
    Errno::result(ret).map_err(|e| anyhow!("set io policy: {}", e))?;

    // also reduce the risk of atime updates causing stalls
    let ret = unsafe {
        setiopolicy_np(
            IOPOL_TYPE_VFS_ATIME_UPDATES,
            IOPOL_SCOPE_THREAD,
            IOPOL_ATIME_UPDATES_OFF,
        )
    };
    Errno::result(ret).map_err(|e| anyhow!("set io policy: {}", e))?;

    Ok(())
}
