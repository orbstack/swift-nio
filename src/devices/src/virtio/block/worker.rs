use crate::legacy::Gic;
use crate::virtio::descriptor_utils::{Reader, Writer};

use super::super::{Queue, VIRTIO_MMIO_INT_VRING};
use super::device::{BlockDevSignalMask, BlockDevWakers, CacheType, DiskProperties};

use gruel::{ParkSignalChannelExt, SignalChannel};
use nix::errno::Errno;
use std::io::{self, Write};
use std::mem::size_of;
use std::result;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};
use utils::Mutex;
use virtio_bindings::virtio_blk::*;
use vm_memory::{ByteValued, GuestMemoryMmap};

const FLUSH_INTERVAL: Duration = Duration::from_millis(1000);

#[derive(Debug)]
pub enum RequestError {
    FlushingToDisk(io::Error),
    InvalidDataLength,
    WriteZeroes(io::Error),
    ReadingFromDescriptor(io::Error),
    WritingToDescriptor(io::Error),
    UnknownRequest,
}

/// The request header represents the mandatory fields of each block device request.
///
/// A request header contains the following fields:
///   * request_type: an u32 value mapping to a read, write or flush operation.
///   * reserved: 32 bits are reserved for future extensions of the Virtio Spec.
///   * sector: an u64 value representing the offset where a read/write is to occur.
///
/// The header simplifies reading the request from memory as all request follow
/// the same memory layout.
#[derive(Copy, Clone, Default)]
#[repr(C)]
pub struct RequestHeader {
    request_type: u32,
    _reserved: u32,
    sector: u64,
}

// Safe because RequestHeader only contains plain data.
unsafe impl ByteValued for RequestHeader {}

pub struct BlockWorker {
    queue: Queue,
    signals: Arc<SignalChannel<BlockDevSignalMask, BlockDevWakers>>,
    interrupt_status: Arc<AtomicUsize>,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,

    mem: GuestMemoryMmap,
    disk: DiskProperties,

    last_flushed_at: Instant,
}

#[repr(C)]
#[derive(Debug, Default, Copy, Clone, PartialEq)]
pub struct VirtioBlkDiscardWriteZeroes {
    pub sector: __le64,
    pub num_sectors: __le32,
    pub flags: __le32,
}
unsafe impl ByteValued for VirtioBlkDiscardWriteZeroes {}

impl BlockWorker {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        queue: Queue,
        signals: Arc<SignalChannel<BlockDevSignalMask, BlockDevWakers>>,
        interrupt_status: Arc<AtomicUsize>,
        intc: Option<Arc<Mutex<Gic>>>,
        irq_line: Option<u32>,
        mem: GuestMemoryMmap,
        disk: DiskProperties,
    ) -> Self {
        Self {
            queue,
            signals,
            interrupt_status,
            intc,
            irq_line,

            mem,
            disk,

            last_flushed_at: Instant::now(),
        }
    }

    pub fn run(self) -> thread::JoinHandle<()> {
        thread::spawn(|| self.work())
    }

    fn work(mut self) {
        let mask = BlockDevSignalMask::MAIN_QUEUE | BlockDevSignalMask::STOP_WORKER;

        loop {
            self.signals.wait_on_park(mask);

            let taken = self.signals.take(mask);

            if taken.intersects(BlockDevSignalMask::MAIN_QUEUE) {
                self.process_virtio_queues();
            }

            if taken.intersects(BlockDevSignalMask::STOP_WORKER) {
                break;
            }
        }
    }

    /// Process device virtio queue(s).
    pub fn process_virtio_queues(&mut self) {
        let mem = self.mem.clone();
        loop {
            self.queue.disable_notification(&mem).unwrap();

            self.process_queue(&mem);

            if !self.queue.enable_notification(&mem).unwrap() {
                break;
            }
        }
    }

    fn process_queue(&mut self, mem: &GuestMemoryMmap) {
        while let Some(head) = self.queue.pop(mem) {
            let mut reader = match Reader::new(mem, head.clone()) {
                Ok(r) => r,
                Err(e) => {
                    error!("invalid descriptor chain: {:?}", e);
                    continue;
                }
            };
            let mut writer = match Writer::new(mem, head.clone()) {
                Ok(r) => r,
                Err(e) => {
                    error!("invalid descriptor chain: {:?}", e);
                    continue;
                }
            };
            let request_header: RequestHeader = match reader.read_obj() {
                Ok(h) => h,
                Err(e) => {
                    error!("invalid request header: {:?}", e);
                    continue;
                }
            };

            let (status, len): (u8, usize) =
                match self.process_request(request_header, &mut reader, &mut writer) {
                    Ok(l) => (VIRTIO_BLK_S_OK.try_into().unwrap(), l),
                    Err(e) => {
                        error!("error processing request: {:?}", e);
                        (VIRTIO_BLK_S_IOERR.try_into().unwrap(), 0)
                    }
                };

            if let Err(e) = writer.write_obj(status) {
                error!("Failed to write virtio block status: {:?}", e)
            }

            if let Err(e) = self.queue.add_used(mem, head.index, len as u32) {
                error!("failed to add used elements to the queue: {:?}", e);
            }

            if self.queue.needs_notification(mem).unwrap() {
                self.signal_used_queue();
            }
        }
    }

    fn process_request(
        &mut self,
        request_header: RequestHeader,
        reader: &mut Reader,
        writer: &mut Writer,
    ) -> result::Result<usize, RequestError> {
        match request_header.request_type {
            VIRTIO_BLK_T_IN => {
                let data_len = writer.available_bytes() - 1;
                if data_len % 512 != 0 {
                    Err(RequestError::InvalidDataLength)
                } else {
                    writer
                        .write_from_at(&self.disk.file, data_len, request_header.sector * 512)
                        .map_err(RequestError::WritingToDescriptor)
                }
            }
            VIRTIO_BLK_T_OUT => {
                let data_len = reader.available_bytes();
                if data_len % 512 != 0 {
                    Err(RequestError::InvalidDataLength)
                } else {
                    reader
                        .read_to_at(&self.disk.file, data_len, request_header.sector * 512)
                        .map_err(RequestError::ReadingFromDescriptor)
                }
            }
            VIRTIO_BLK_T_DISCARD | VIRTIO_BLK_T_WRITE_ZEROES => {
                while reader.available_bytes() >= size_of::<VirtioBlkDiscardWriteZeroes>() {
                    let seg: VirtioBlkDiscardWriteZeroes = reader
                        .read_obj()
                        .map_err(RequestError::ReadingFromDescriptor)?;

                    let offset = seg.sector * 512;
                    let len = (seg.num_sectors * 512) as u64;
                    if offset + len > self.disk.nsectors() * 512 {
                        return Err(RequestError::InvalidDataLength);
                    }

                    match self.disk.punch_hole(offset as usize, len as usize) {
                        Ok(_) => (),
                        Err(_) => {
                            // only diff: DISCARD fails gracefully and is ignored; WRITE_ZEROES needs a fallback if not supported by FS
                            if request_header.request_type == VIRTIO_BLK_T_WRITE_ZEROES {
                                // TODO fallback. we only support APFS so this is impossible
                                return Err(RequestError::WriteZeroes(Errno::EOPNOTSUPP.into()));
                            } else {
                                // ignore error
                            }
                        }
                    }
                }
                Ok(reader.bytes_read())
            }
            VIRTIO_BLK_T_FLUSH => match self.disk.cache_type() {
                CacheType::Writeback => {
                    // F_FULLFSYNC is very expensive on Apple SSDs, so only do it every 1000ms (leading edge)
                    // barrier suffices for integrity; F_FULLFSYNC is only for persistence on shutdown
                    if Instant::now() - self.last_flushed_at > FLUSH_INTERVAL {
                        let diskfile = self.disk.file_mut();
                        diskfile.sync_all().map_err(RequestError::FlushingToDisk)?;
                        // get timestamp *after* sync
                        // clock_gettime is much cheaper than fsync
                        self.last_flushed_at = Instant::now();
                    } else {
                        self.disk
                            .fsync_barrier()
                            .map_err(RequestError::FlushingToDisk)?;
                    }
                    Ok(0)
                }
                CacheType::Unsafe => Ok(0),
            },
            VIRTIO_BLK_T_GET_ID => {
                let data_len = writer.available_bytes();
                let disk_id = self.disk.image_id();
                if data_len < disk_id.len() {
                    Err(RequestError::InvalidDataLength)
                } else {
                    writer
                        .write_all(disk_id)
                        .map_err(RequestError::WritingToDescriptor)?;
                    Ok(disk_id.len())
                }
            }
            _ => Err(RequestError::UnknownRequest),
        }
    }

    fn signal_used_queue(&self) {
        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);
        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
        } else {
            self.signals.assert(BlockDevSignalMask::INTERRUPT);
        }
    }
}
