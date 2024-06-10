extern "C" {
    fn setiopolicy_np(iotype: libc::c_int, scope: libc::c_int, policy: libc::c_int) -> libc::c_int;
}

const IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES: libc::c_int = 3;

const IOPOL_SCOPE_THREAD: libc::c_int = 1;

const IOPOL_MATERIALIZE_DATALESS_FILES_OFF: libc::c_int = 1;

pub fn prepare_vcpu_for_hvc() -> anyhow::Result<()> {
    // don't allow materializing dataless files:
    // it can block for ~1 sec, which is bad for vCPU thread
    let ret = unsafe {
        setiopolicy_np(
            IOPOL_TYPE_VFS_MATERIALIZE_DATALESS_FILES,
            IOPOL_SCOPE_THREAD,
            IOPOL_MATERIALIZE_DATALESS_FILES_OFF,
        )
    };
    if ret != 0 {
        return Err(anyhow::anyhow!("failed to set io policy: {}", ret));
    }

    Ok(())
}
