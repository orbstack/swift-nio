mod device;
#[allow(dead_code)]
mod filesystem;
pub mod fuse;
mod hvc;
#[allow(dead_code)]
mod multikey;
mod server;
mod worker;

#[cfg(target_os = "linux")]
pub mod linux;
#[cfg(target_os = "linux")]
pub use linux::fs_utils;
#[cfg(target_os = "linux")]
pub use linux::passthrough;
#[cfg(target_os = "macos")]
pub mod macos;
#[cfg(target_os = "macos")]
pub use macos::fs_utils;
#[cfg(target_os = "macos")]
pub use macos::passthrough;
use rustc_hash::FxHasher;
#[cfg(target_os = "macos")]
pub mod rosetta;

use super::bindings;
use super::descriptor_utils;
use serde::{Deserialize, Serialize};

pub use self::defs::uapi::VIRTIO_ID_FS as TYPE_FS;
pub use self::device::Fs;

mod defs {
    pub const FS_DEV_ID: &str = "virtio_fs";
    pub const NUM_QUEUES: usize = 2;
    pub const QUEUE_SIZES: &[u16] = &[1024; NUM_QUEUES];
    // High priority queue.
    pub const HPQ_INDEX: usize = 0;
    // Request queue.
    pub const REQ_INDEX: usize = 1;

    pub mod uapi {
        pub const VIRTIO_ID_FS: u32 = 26;
    }
}

use std::ffi::{FromBytesWithNulError, FromVecWithNulError};
use std::hash::BuildHasherDefault;
use std::io;

use descriptor_utils::Error as DescriptorError;

#[derive(Debug)]
pub enum FsError {
    /// Failed to decode protocol messages.
    DecodeMessage(io::Error),
    /// Failed to encode protocol messages.
    EncodeMessage(io::Error),
    /// Failed to create event fd.
    EventFd(std::io::Error),
    /// Failed to create server.
    CreateServer(std::io::Error),
    /// The guest failed to send a require extensions.
    MissingExtension,
    /// One or more parameters are missing.
    MissingParameter,
    /// A C string parameter is invalid.
    InvalidCString(FromBytesWithNulError),
    InvalidCString2(FromVecWithNulError),
    /// The `len` field of the header is too small.
    InvalidHeaderLength,
    /// The `size` field of the `SetxattrIn` message does not match the length
    /// of the decoded value.
    InvalidXattrSize((u32, usize)),
    QueueReader(DescriptorError),
    QueueWriter(DescriptorError),
}

#[derive(PartialEq, Eq, Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NfsInfo {
    dir_dev: i32,
    dir_inode: u64,
    dir_name: String,
    parent_dir_dev: i32,
    parent_dir_inode: u64,
    empty_dir_inode: u64,
}

type Result<T> = std::result::Result<T, FsError>;

pub(crate) type FxDashMap<K, V> = dashmap::DashMap<K, V, BuildHasherDefault<FxHasher>>;
