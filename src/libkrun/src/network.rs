use std::{
    ffi::c_void,
    sync::{Arc, RwLock},
};

use devices::virtio::{
    descriptor_utils::Iovec,
    net::{HostNetCallbacks, NetCallbacks},
};
use libc::{EBADF, EPIPE};
use nix::errno::Errno;

extern "C" {
    fn rsvm_go_gvisor_network_write_packet(
        handle: *mut c_void,
        iovs: *const libc::iovec,
        num_iovs: usize,
        total_len: usize,
    ) -> i32;

    fn swext_network_write_packet(
        handle: *mut c_void,
        iovs: *const libc::iovec,
        num_iovs: usize,
        total_len: usize,
    ) -> i32;
}

// Rust/krun handle -> virtio-net device, for Swift and Go to send packets to the guest (RX path) using rsvm_network_write_packet
// Swift, Go, and Rust each have their own handle namespaces, so a single device may have differing TX and RX handles
pub static NETDEV_HANDLES: RwLock<HandleRegistry> = RwLock::new(HandleRegistry { handles: vec![] });

pub struct HandleRegistry {
    handles: Vec<Option<Arc<dyn NetCallbacks>>>,
}

impl HandleRegistry {
    pub fn new_handle(&mut self) -> usize {
        let idx = self.handles.len();
        self.handles.push(None);
        idx
    }
}

pub struct GoNetCallbacks {
    pub(crate) handle: *mut c_void,
    pub(crate) rust_handle_index: usize,
}

unsafe impl Send for GoNetCallbacks {}
unsafe impl Sync for GoNetCallbacks {}

impl NetCallbacks for GoNetCallbacks {
    fn write_packet(&self, iovs: &[Iovec], len: usize) -> nix::Result<()> {
        let ret = unsafe {
            rsvm_go_gvisor_network_write_packet(
                self.handle,
                iovs.as_ptr() as *const _,
                iovs.len(),
                len,
            )
        };
        Errno::result(ret)?;
        Ok(())
    }
}

impl HostNetCallbacks for GoNetCallbacks {
    fn set_guest_callbacks(&self, callbacks: Option<Arc<dyn NetCallbacks>>) {
        let mut reg = NETDEV_HANDLES.write().unwrap();
        reg.handles[self.rust_handle_index] = callbacks;
    }
}

pub struct SwiftNetCallbacks {
    pub(crate) handle: *mut c_void,
    pub(crate) rust_handle_index: usize,
}

unsafe impl Send for SwiftNetCallbacks {}
unsafe impl Sync for SwiftNetCallbacks {}

impl NetCallbacks for SwiftNetCallbacks {
    fn write_packet(&self, iovs: &[Iovec], len: usize) -> nix::Result<()> {
        let ret = unsafe {
            swext_network_write_packet(self.handle, iovs.as_ptr() as *const _, iovs.len(), len)
        };
        Errno::result(ret)?;
        Ok(())
    }
}

impl HostNetCallbacks for SwiftNetCallbacks {
    fn set_guest_callbacks(&self, callbacks: Option<Arc<dyn NetCallbacks>>) {
        let mut reg = NETDEV_HANDLES.write().unwrap();
        reg.handles[self.rust_handle_index] = callbacks;
    }
}

#[no_mangle]
pub unsafe extern "C" fn rsvm_network_write_packet(
    handle: *mut c_void,
    iovs: *const libc::iovec,
    num_iovs: usize,
    total_len: usize,
) -> i32 {
    let iovs = std::slice::from_raw_parts(iovs, num_iovs);
    let iovs = std::mem::transmute::<&[libc::iovec], &[Iovec]>(iovs);

    let handle_idx = handle as usize;
    let handles = NETDEV_HANDLES.read().unwrap();
    let Some(cb) = handles.handles.get(handle_idx) else {
        return -EBADF;
    };
    let Some(cb) = cb.clone() else {
        return -EPIPE;
    };
    drop(handles);

    if let Err(e) = cb.write_packet(iovs, total_len) {
        -(e as i32)
    } else {
        0
    }
}
