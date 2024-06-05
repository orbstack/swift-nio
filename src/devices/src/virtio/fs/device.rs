use bitflags::bitflags;
use gruel::{define_waker_set, BoundSignalChannelRef, ParkWaker, SignalChannel};
use newt::{make_bit_flag_range, BitFlagRange};
use std::cmp;
use std::io::Write;
use std::sync::atomic::AtomicUsize;
use std::sync::Arc;
use std::thread::JoinHandle;
use utils::Mutex;

use virtio_bindings::{virtio_config::VIRTIO_F_VERSION_1, virtio_ring::VIRTIO_RING_F_EVENT_IDX};
use vm_memory::{ByteValued, GuestMemoryMmap};

use super::super::{
    ActivateResult, DeviceState, FsError, Queue as VirtQueue, VirtioDevice, VirtioShmRegion,
};
use super::hvc::FsHvcDevice;
use super::macos::passthrough::PassthroughFs;
use super::server::Server;
use super::worker::FsWorker;
use super::{defs, defs::uapi};
use super::{passthrough, FsCallbacks, NfsInfo};
use crate::legacy::Gic;
use crate::virtio::VirtioQueueSignals;

define_waker_set! {
    #[derive(Default)]
    pub(crate) struct FsWakers {
        park: ParkWaker,
    }
}

bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub(crate) struct FsSignalMask: u64 {
        const INTERRUPT = 1 << 0;
        const SHUTDOWN_WORKER = 1 << 1;
        const QUEUES = u64::MAX << 2;
    }
}

pub(crate) const FS_QUEUE_SIGS: BitFlagRange<FsSignalMask> =
    make_bit_flag_range!(mask FsSignalMask::QUEUES);

pub(crate) type FsSignalChannel = SignalChannel<FsSignalMask, FsWakers>;

#[derive(Copy, Clone)]
#[repr(C, packed)]
struct VirtioFsConfig {
    tag: [u8; 36],
    num_request_queues: u32,
}

impl Default for VirtioFsConfig {
    fn default() -> Self {
        VirtioFsConfig {
            tag: [0; 36],
            num_request_queues: 0,
        }
    }
}

unsafe impl ByteValued for VirtioFsConfig {}

pub struct Fs {
    queues: Vec<VirtQueue>,
    signals: Arc<FsSignalChannel>,
    avail_features: u64,
    acked_features: u64,
    interrupt_status: Arc<AtomicUsize>,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,
    device_state: DeviceState,
    config: VirtioFsConfig,
    shm_region: Option<VirtioShmRegion>,
    worker_thread: Option<JoinHandle<()>>,
    server: Arc<Server<PassthroughFs>>,
}

impl Fs {
    pub(crate) fn with_queues(
        fs_id: String,
        shared_dir: String,
        nfs_info: Option<NfsInfo>,
        queues: Vec<VirtQueue>,
        callbacks: Option<Arc<dyn FsCallbacks>>,
    ) -> super::Result<Fs> {
        let avail_features = (1u64 << VIRTIO_F_VERSION_1) | (1u64 << VIRTIO_RING_F_EVENT_IDX);

        let allow_rosetta_ioctl = fs_id == "rosetta";
        let tag = fs_id.into_bytes();
        let mut config = VirtioFsConfig::default();
        config.tag[..tag.len()].copy_from_slice(tag.as_slice());
        config.num_request_queues = 1;

        let fs_cfg = passthrough::Config {
            root_dir: shared_dir,
            allow_rosetta_ioctl,
            nfs_info,
            ..Default::default()
        };

        Ok(Fs {
            queues,
            signals: Arc::new(SignalChannel::new(FsWakers::default())),
            avail_features,
            acked_features: 0,
            interrupt_status: Arc::new(AtomicUsize::new(0)),
            intc: None,
            irq_line: None,
            device_state: DeviceState::Inactive,
            config,
            shm_region: None,
            worker_thread: None,
            server: Arc::new(Server::new(
                PassthroughFs::new(fs_cfg, callbacks.clone()).map_err(FsError::CreateServer)?,
            )),
        })
    }

    pub fn new(
        fs_id: String,
        shared_dir: String,
        nfs_info: Option<NfsInfo>,
        activity_notifier: Option<Arc<dyn FsCallbacks>>,
    ) -> super::Result<Fs> {
        let queues: Vec<VirtQueue> = defs::QUEUE_SIZES
            .iter()
            .map(|&max_size| VirtQueue::new(max_size))
            .collect();
        Self::with_queues(fs_id, shared_dir, nfs_info, queues, activity_notifier)
    }

    pub fn id(&self) -> &str {
        defs::FS_DEV_ID
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.intc = Some(intc);
    }

    pub fn set_shm_region(&mut self, shm_region: VirtioShmRegion) {
        self.shm_region = Some(shm_region);
    }

    pub fn create_hvc_device(&self, mem: GuestMemoryMmap) -> FsHvcDevice {
        FsHvcDevice::new(mem, self.server.clone())
    }
}

impl VirtioDevice for Fs {
    fn avail_features(&self) -> u64 {
        self.avail_features
    }

    fn acked_features(&self) -> u64 {
        self.acked_features
    }

    fn set_acked_features(&mut self, acked_features: u64) {
        self.acked_features = acked_features
    }

    fn device_type(&self) -> u32 {
        uapi::VIRTIO_ID_FS
    }

    fn queues(&self) -> &[VirtQueue] {
        &self.queues
    }

    fn queues_mut(&mut self) -> &mut [VirtQueue] {
        &mut self.queues
    }

    fn queue_signals(&self) -> VirtioQueueSignals {
        VirtioQueueSignals::new(self.signals.clone(), FS_QUEUE_SIGS)
    }

    fn interrupt_signal(&self) -> BoundSignalChannelRef<'_> {
        BoundSignalChannelRef::new(&*self.signals, FsSignalMask::INTERRUPT)
    }

    fn interrupt_status(&self) -> Arc<AtomicUsize> {
        self.interrupt_status.clone()
    }

    fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(irq);
    }

    fn read_config(&self, offset: u64, mut data: &mut [u8]) {
        let config_slice = self.config.as_slice();
        let config_len = config_slice.len() as u64;
        if offset >= config_len {
            error!("Failed to read config space");
            return;
        }
        if let Some(end) = offset.checked_add(data.len() as u64) {
            // This write can't fail, offset and end are checked against config_len.
            data.write_all(&config_slice[offset as usize..cmp::min(end, config_len) as usize])
                .unwrap();
        }
    }

    fn write_config(&mut self, offset: u64, data: &[u8]) {
        warn!(
            "fs: guest driver attempted to write device config (offset={:x}, len={:x})",
            offset,
            data.len()
        );
    }

    fn activate(&mut self, mem: GuestMemoryMmap) -> ActivateResult {
        if self.worker_thread.is_some() {
            panic!("virtio_fs: worker thread already exists");
        }

        let event_idx: bool = (self.acked_features & (1 << VIRTIO_RING_F_EVENT_IDX)) != 0;
        self.queues[defs::HPQ_INDEX].set_event_idx(event_idx);
        self.queues[defs::REQ_INDEX].set_event_idx(event_idx);

        let worker = FsWorker::new(
            self.signals.clone(),
            self.queues.clone(),
            self.interrupt_status.clone(),
            self.intc.clone(),
            self.irq_line,
            mem.clone(),
            self.server.clone(),
        );
        self.worker_thread = Some(worker.run());

        self.device_state = DeviceState::Activated(mem);
        Ok(())
    }

    fn is_activated(&self) -> bool {
        match self.device_state {
            DeviceState::Inactive => false,
            DeviceState::Activated(_) => true,
        }
    }

    fn shm_region(&self) -> Option<&VirtioShmRegion> {
        self.shm_region.as_ref()
    }

    fn reset(&mut self) -> bool {
        if let Some(worker) = self.worker_thread.take() {
            self.signals.assert(FsSignalMask::SHUTDOWN_WORKER);

            if let Err(e) = worker.join() {
                error!("error waiting for worker thread: {:?}", e);
            }
        }
        self.device_state = DeviceState::Inactive;
        true
    }
}
