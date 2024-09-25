use devices::virtio::FsCallbacks;

use crate::result::GResultErr;

extern "C" {
    fn swext_fsevents_cb_krpc_events(krpc_buf: *const u8, krpc_buf_len: usize);
}

#[derive(Debug)]
pub struct GoFsCallbacks {}

impl FsCallbacks for GoFsCallbacks {
    fn send_krpc_events(&self, krpc_buf: &[u8]) {
        unsafe {
            swext_fsevents_cb_krpc_events(krpc_buf.as_ptr(), krpc_buf.len());
        }
    }
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_set_rinit_data(ptr: *const u8, size: usize) -> GResultErr {
    let res = devices::virtio::fs::rosetta::set_rosetta_data(unsafe {
        std::slice::from_raw_parts(ptr, size)
    });
    GResultErr::from_result(res)
}
