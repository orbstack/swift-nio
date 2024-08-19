// Copyright 2020 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Portions Copyright 2017 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the THIRD-PARTY file.

use bitflags::bitflags;
use filemap::MappedFile;
use gruel::{
    define_waker_set, ArcBoundSignalChannel, BoundSignalChannel, ParkWaker, SignalChannel,
};
use nix::errno::Errno;
use nix::sys::uio::pwritev;
use std::cmp;
use std::convert::From;
use std::fs::{File, OpenOptions};
use std::io::{self, Write};
use std::os::fd::AsRawFd;
#[cfg(target_os = "linux")]
use std::os::linux::fs::MetadataExt;
#[cfg(target_os = "macos")]
use std::os::macos::fs::MetadataExt;
use std::path::PathBuf;
use std::result;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, RwLock};
use std::thread::JoinHandle;
use std::time::Duration;
use utils::time::{get_time, ClockType};
use utils::Mutex;

use libc::{fpunchhole_t, off_t};
use tracing::{error, warn};
use virtio_bindings::{
    virtio_blk::*, virtio_config::VIRTIO_F_VERSION_1, virtio_ring::VIRTIO_RING_F_EVENT_IDX,
};
use vm_memory::{ByteValued, GuestMemoryMmap};

use super::hvc::BlockHvcDevice;
use super::worker::BlockWorker;
use super::QUEUE_SIZE;
use super::{
    super::{ActivateResult, DeviceState, Queue, VirtioDevice, TYPE_BLOCK},
    Error, SECTOR_SHIFT, SECTOR_SIZE,
};

use crate::legacy::Gic;
use crate::virtio::descriptor_utils::Iovec;
use crate::virtio::{ErasedSyncEventHandlerSet, SyncEventHandlerSet};

const FLUSH_INTERVAL_NS: u64 = Duration::from_millis(1000).as_nanos() as u64;

const USE_ASYNC_WORKER: bool = true;

define_waker_set! {
    #[derive(Default)]
    pub struct BlockDevWakers {
        parker: ParkWaker,
    }
}

bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub struct BlockDevSignalMask: u64 {
        const STOP_WORKER = 1 << 0;
        const REQ = 1 << 1;
    }
}

/// Configuration options for disk caching.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub enum CacheType {
    /// Flushing mechanic will be advertised to the guest driver, but
    /// the operation will be a noop.
    #[default]
    Unsafe,
    /// Flushing mechanic will be advertised to the guest driver and
    /// flush requests coming from the guest will be performed using
    /// `fsync`.
    Writeback,
}

/// Helper object for setting up all `Block` fields derived from its backing file.
pub(crate) struct DiskProperties {
    cache_type: CacheType,
    mapped_file: MappedFile,
    nsectors: u64,
    image_id: Vec<u8>,
    read_only: bool,
    fs_block_size: u64,

    last_flushed_at: AtomicU64,
}

impl DiskProperties {
    pub fn new(
        disk_image_path: String,
        is_disk_read_only: bool,
        cache_type: CacheType,
    ) -> io::Result<Self> {
        let disk_image = OpenOptions::new()
            .read(true)
            .write(!is_disk_read_only)
            .open(PathBuf::from(&disk_image_path))?;
        let disk_metadata = disk_image.metadata()?;
        let disk_size = disk_metadata.len();
        let fs_block_size = disk_metadata.st_blksize();

        // We only support disk size, which uses the first two words of the configuration space.
        // If the image is not a multiple of the sector size, the tail bits are not exposed.
        if disk_size % SECTOR_SIZE != 0 {
            warn!(
                "Disk size {} is not a multiple of sector size {}; \
                 the remainder will not be visible to the guest.",
                disk_size, SECTOR_SIZE
            );
        }

        if fs_block_size % SECTOR_SIZE != 0 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                format!(
                    "File system block size {} is not a multiple of sector size {}",
                    fs_block_size, SECTOR_SIZE
                ),
            ));
        }

        let image_id = Self::build_disk_image_id(&disk_image);
        let mapped_file = MappedFile::new(disk_image, disk_size as usize)?;

        Ok(Self {
            cache_type,
            nsectors: disk_size >> SECTOR_SHIFT,
            image_id,
            mapped_file,
            read_only: is_disk_read_only,
            fs_block_size,
            // this is technically wrong on system boot, but it doesn't matter
            last_flushed_at: AtomicU64::new(0),
        })
    }

    fn file(&self) -> &File {
        self.mapped_file.file()
    }

    pub fn nsectors(&self) -> u64 {
        self.nsectors
    }

    pub fn size(&self) -> u64 {
        self.nsectors * SECTOR_SIZE
    }

    pub fn image_id(&self) -> &[u8] {
        &self.image_id
    }

    pub fn read_to_iovec(&self, offset: usize, iov: &Iovec) -> io::Result<usize> {
        // mapped_file does bounds check
        self.mapped_file.read(offset, iov.as_std_mut())
    }

    pub fn write_iovecs(&self, offset: u64, iovecs: &[Iovec]) -> io::Result<usize> {
        // bounds check
        let len = iovecs.iter().map(|iov| iov.len()).sum::<usize>();
        if offset.saturating_add(len as u64) > self.size() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "write out of bounds",
            ));
        }

        let n = pwritev(
            self.file().as_raw_fd(),
            Iovec::slice_to_std(iovecs),
            offset as i64,
        )?;
        Ok(n)
    }

    fn fsync_barrier(&self) -> std::io::Result<()> {
        let ret = unsafe { libc::fcntl(self.file().as_raw_fd(), libc::F_BARRIERFSYNC, 0) };
        Errno::result(ret)?;
        Ok(())
    }

    pub fn flush(&self) -> std::io::Result<()> {
        match self.cache_type {
            CacheType::Unsafe => {
                // noop
                return Ok(());
            }
            CacheType::Writeback => {
                // continue and flush
            }
        }

        // F_FULLFSYNC is very expensive on Apple SSDs, so only do it every 1000ms (leading edge)
        // barrier suffices for integrity; F_FULLFSYNC is only for persistence on shutdown
        if get_time(ClockType::Monotonic) - self.last_flushed_at.load(Ordering::Relaxed)
            > FLUSH_INTERVAL_NS
        {
            self.file().sync_all()?;

            // save timestamp *after* sync:
            // fsync is slow, so a lot of time may have passed. make sure we don't flush again too early
            self.last_flushed_at
                .store(get_time(ClockType::Monotonic), Ordering::Relaxed);
        } else {
            self.fsync_barrier()?;
        }

        Ok(())
    }

    pub fn punch_hole(&self, offset: u64, len: u64) -> std::io::Result<()> {
        // bounds check
        if offset.saturating_add(len) > self.size() {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "punch hole out of bounds",
            ));
        }

        let punchhole = fpunchhole_t {
            fp_flags: 0,
            reserved: 0,
            fp_offset: offset as off_t,
            fp_length: len as off_t,
        };

        let ret = unsafe { libc::fcntl(self.file().as_raw_fd(), libc::F_PUNCHHOLE, &punchhole) };
        Errno::result(ret)?;
        Ok(())
    }

    fn build_device_id(disk_file: &File) -> result::Result<String, Error> {
        let blk_metadata = disk_file.metadata().map_err(Error::GetFileMetadata)?;
        // This is how kvmtool does it.
        let device_id = format!(
            "{}{}{}",
            blk_metadata.st_dev(),
            blk_metadata.st_rdev(),
            blk_metadata.st_ino()
        );
        Ok(device_id)
    }

    fn build_disk_image_id(disk_file: &File) -> Vec<u8> {
        let mut default_id = vec![0; VIRTIO_BLK_ID_BYTES as usize];
        match Self::build_device_id(disk_file) {
            Err(_) => {
                warn!("Could not generate device id. We'll use a default.");
            }
            Ok(m) => {
                // The kernel only knows to read a maximum of VIRTIO_BLK_ID_BYTES.
                // This will also zero out any leftover bytes.
                let disk_id = m.as_bytes();
                let bytes_to_copy = cmp::min(disk_id.len(), VIRTIO_BLK_ID_BYTES as usize);
                default_id[..bytes_to_copy].clone_from_slice(&disk_id[..bytes_to_copy])
            }
        }
        default_id
    }
}

impl Drop for DiskProperties {
    fn drop(&mut self) {
        if self.read_only {
            return;
        }

        match self.cache_type {
            CacheType::Writeback => {
                // Sync data out to physical media on host.
                if self.file().sync_all().is_err() {
                    error!("Failed to sync block data on drop.")
                }
            }
            CacheType::Unsafe => {
                // This is a noop.
            }
        };
    }
}

#[derive(Copy, Clone, Debug, Default)]
#[repr(C, packed)]
struct VirtioBlkGeometry {
    cylinders: u16,
    heads: u8,
    sectors: u8,
}

#[derive(Copy, Clone, Debug, Default)]
#[repr(C, packed)]
struct VirtioBlkTopology {
    physical_block_exp: u8,
    alignment_offset: u8,
    min_io_size: u16,
    opt_io_size: u32,
}

#[derive(Copy, Clone, Debug, Default)]
#[repr(C, packed)]
struct VirtioBlkConfig {
    capacity: u64,
    size_max: u32,
    seg_max: u32,
    geometry: VirtioBlkGeometry,
    blk_size: u32,
    topology: VirtioBlkTopology,
    writeback: u8,
    unused0: u8,
    num_queues: u16,
    max_discard_sectors: u32,
    max_discard_seg: u32,
    discard_sector_alignment: u32,
    max_write_zeroes_sectors: u32,
    max_write_zeroes_seg: u32,
    write_zeroes_may_unmap: u8,
    unused1: [u8; 3],
}

// Safe because it only has data and has no implicit padding.
unsafe impl ByteValued for VirtioBlkConfig {}

/// Virtio device for exposing block level read/write operations on a host file.
pub struct Block {
    // Host file and properties.
    disk: Arc<DiskProperties>,
    worker_mode: BlockWorkerMode,

    // Virtio fields.
    pub(crate) avail_features: u64,
    pub(crate) acked_features: u64,
    config: VirtioBlkConfig,

    // Transport related fields.
    pub(crate) queues: Box<[Queue]>,
    pub(crate) signals: Box<[Arc<SignalChannel<BlockDevSignalMask, BlockDevWakers>>]>,
    pub(crate) device_state: DeviceState,

    // Implementation specific fields.
    pub(crate) id: String,
    pub(crate) partuuid: Option<String>,

    // Interrupt specific fields.
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<BlockIrqMode>,
}

enum BlockWorkerMode {
    Sync(BlockSyncWorkerSet),
    Async(Option<Vec<JoinHandle<()>>>),
}

#[derive(Debug, Copy, Clone)]
enum BlockIrqMode {
    Single(u32),
    Range(u32),
}

impl Block {
    /// Create a new virtio block device that operates on the given file.
    ///
    /// The given file must be seekable and sizable.
    pub fn new(
        id: String,
        partuuid: Option<String>,
        cache_type: CacheType,
        disk_image_path: String,
        is_disk_read_only: bool,
        vcpu_count: usize,
    ) -> io::Result<Block> {
        let disk_properties =
            DiskProperties::new(disk_image_path.clone(), is_disk_read_only, cache_type)?;

        let mut avail_features = (1u64 << VIRTIO_F_VERSION_1)
            | (1u64 << VIRTIO_BLK_F_FLUSH)
            | (1u64 << VIRTIO_BLK_F_DISCARD)
            | (1u64 << VIRTIO_BLK_F_WRITE_ZEROES)
            | (1u64 << VIRTIO_BLK_F_SEG_MAX)
            | (1u64 << VIRTIO_RING_F_EVENT_IDX)
            | (1u64 << VIRTIO_BLK_F_MQ);

        if is_disk_read_only {
            avail_features |= 1u64 << VIRTIO_BLK_F_RO;
        };

        let queue_count = vcpu_count;
        let queues = (0..queue_count)
            .map(|_| Queue::new(QUEUE_SIZE))
            .collect::<Box<_>>();

        let config = VirtioBlkConfig {
            capacity: disk_properties.nsectors(),
            size_max: 0,
            num_queues: queues.len() as u16,
            seg_max: QUEUE_SIZE as u32 - 2,
            max_discard_sectors: u32::MAX,
            max_discard_seg: QUEUE_SIZE as u32 - 2,
            // holes must be aligned to FS block size
            discard_sector_alignment: (disk_properties.fs_block_size / SECTOR_SIZE) as u32,
            max_write_zeroes_sectors: u32::MAX,
            max_write_zeroes_seg: QUEUE_SIZE as u32 - 2,
            write_zeroes_may_unmap: 1,
            ..Default::default()
        };

        Ok(Block {
            id,
            partuuid,
            config,
            disk: Arc::new(disk_properties),
            avail_features,
            acked_features: 0u64,
            queues,
            signals: (0..queue_count)
                .map(|_| Arc::new(SignalChannel::new(BlockDevWakers::default())))
                .collect::<Box<_>>(),
            device_state: DeviceState::Inactive,
            intc: None,
            irq_line: None,
            worker_mode: if USE_ASYNC_WORKER {
                BlockWorkerMode::Async(None)
            } else {
                BlockWorkerMode::Sync(BlockSyncWorkerSet(Arc::new(RwLock::new(Box::new([])))))
            },
        })
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.intc = Some(intc);
    }

    /// Provides the ID of this block device.
    pub fn id(&self) -> &String {
        &self.id
    }

    /// Provides the PARTUUID of this block device.
    pub fn partuuid(&self) -> Option<&String> {
        self.partuuid.as_ref()
    }

    /// Specifies if this block device is read only.
    pub fn is_read_only(&self) -> bool {
        self.avail_features & (1u64 << VIRTIO_BLK_F_RO) != 0
    }

    pub fn create_hvc_device(&self, mem: GuestMemoryMmap, index: usize) -> BlockHvcDevice {
        BlockHvcDevice::new(mem, self.disk.clone(), index)
    }
}

impl VirtioDevice for Block {
    fn device_type(&self) -> u32 {
        TYPE_BLOCK
    }

    fn queues(&self) -> &[Queue] {
        &self.queues
    }

    fn queues_mut(&mut self) -> &mut [Queue] {
        &mut self.queues
    }

    fn queue_signals(&self) -> Vec<ArcBoundSignalChannel> {
        self.signals
            .iter()
            .map(|s| BoundSignalChannel::new(s.clone(), BlockDevSignalMask::REQ))
            .collect()
    }

    fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(BlockIrqMode::Single(irq));
    }

    fn set_irq_line_mq(&mut self, first_queue_irq: u32) {
        self.irq_line = Some(BlockIrqMode::Range(first_queue_irq));
    }

    fn avail_features(&self) -> u64 {
        self.avail_features
    }

    fn acked_features(&self) -> u64 {
        self.acked_features
    }

    fn set_acked_features(&mut self, acked_features: u64) {
        self.acked_features = acked_features;
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

    fn write_config(&mut self, _offset: u64, _data: &[u8]) {
        error!("Guest attempted to write config");
    }

    fn is_activated(&self) -> bool {
        match self.device_state {
            DeviceState::Inactive => false,
            DeviceState::Activated(_) => true,
        }
    }

    fn activate(&mut self, mem: GuestMemoryMmap) -> ActivateResult {
        assert!(matches!(self.device_state, DeviceState::Inactive));

        let mut workers = Vec::new();
        let irq_line = self
            .irq_line
            .expect("`irq_line` never initialized before activation");

        for ((queue_index, queue), signal) in
            self.queues.iter_mut().enumerate().zip(self.signals.iter())
        {
            let event_idx: bool = (self.acked_features & (1 << VIRTIO_RING_F_EVENT_IDX)) != 0;
            queue.set_event_idx(event_idx);

            workers.push(BlockWorker::new(
                queue.clone(),
                signal.clone(),
                self.intc.clone(),
                match irq_line {
                    BlockIrqMode::Single(single) => single,
                    BlockIrqMode::Range(base) => base + queue_index as u32,
                },
                queue_index as u64,
                mem.clone(),
                self.disk.clone(),
            ));
        }

        match &mut self.worker_mode {
            BlockWorkerMode::Sync(state) => {
                *state.0.write().unwrap() = workers.into_iter().map(Mutex::new).collect();
            }
            BlockWorkerMode::Async(state) => {
                *state = Some(workers.into_iter().map(|w| w.run()).collect());
            }
        }

        self.device_state = DeviceState::Activated(mem);
        Ok(())
    }

    fn reset(&mut self) -> bool {
        match &mut self.worker_mode {
            BlockWorkerMode::Sync(state) => {
                *state.0.write().unwrap() = Box::new([]);
            }
            BlockWorkerMode::Async(worker) => {
                for signal in self.signals.iter() {
                    signal.assert(BlockDevSignalMask::STOP_WORKER);
                }

                if let Some(workers) = worker.take() {
                    for worker in workers {
                        if let Err(e) = worker.join() {
                            error!("error waiting for worker thread: {:?}", e);
                        }
                    }
                }
            }
        }

        self.device_state = DeviceState::Inactive;
        true
    }

    fn sync_events(&self) -> Option<ErasedSyncEventHandlerSet> {
        match &self.worker_mode {
            BlockWorkerMode::Sync(state) => Some(smallbox::smallbox!(state.clone())),
            BlockWorkerMode::Async(_) => None,
        }
    }
}

#[derive(Clone)]
struct BlockSyncWorkerSet(Arc<RwLock<Box<[Mutex<BlockWorker>]>>>);

impl SyncEventHandlerSet for BlockSyncWorkerSet {
    fn process(&self, _vcpuid: u64, queue: u32) {
        if let Some(worker) = self.0.read().unwrap().get(queue as usize) {
            worker.lock().unwrap().process_virtio_queues();
        }
    }
}
