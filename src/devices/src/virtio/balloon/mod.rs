mod device;
mod event_handler;

pub use self::defs::uapi::VIRTIO_ID_BALLOON as TYPE_BALLOON;
pub use self::device::Balloon;

mod defs {
    pub const BALLOON_DEV_ID: &str = "virtio_balloon";
    pub const NUM_QUEUES: usize = 5;
    pub const QUEUE_SIZES: &[u16] = &[256; NUM_QUEUES];

    pub mod uapi {
        pub const VIRTIO_F_VERSION_1: u32 = 32;
        pub const VIRTIO_ID_BALLOON: u32 = 5;
        pub const VIRTIO_BALLOON_F_STATS_VQ: u32 = 1;
        pub const VIRTIO_BALLOON_F_FREE_PAGE_HINT: u32 = 3;
        pub const VIRTIO_BALLOON_F_REPORTING: u32 = 5;
    }
}

#[derive(Debug)]
pub enum BalloonError {
    /// Failed to create event fd.
    EventFd(std::io::Error),
}

type Result<T> = std::result::Result<T, BalloonError>;
