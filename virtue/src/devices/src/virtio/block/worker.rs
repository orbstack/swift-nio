use crate::legacy::Gic;
use crate::virtio::descriptor_utils::{Reader, Writer};

use super::super::Queue;
use super::device::{BlockDevSignalMask, BlockDevWakers, DiskProperties};
use super::SECTOR_SIZE;

use bytemuck::{Pod, Zeroable};
use gruel::{ParkSignalChannelExt, SignalChannel};
use nix::errno::Errno;
use std::io::{self, Write};
use std::mem::size_of;
use std::result;
use std::sync::Arc;
use std::thread;
use utils::memory::GuestMemory;
use utils::qos::QosClass;
use virtio_bindings::virtio_blk::*;

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
#[derive(Copy, Clone, Default, Pod, Zeroable)]
#[repr(C)]
pub struct RequestHeader {
    request_type: u32,
    _reserved: u32,
    sector: u64,
}

pub struct BlockWorker {
    queue: Queue,
    signals: Arc<SignalChannel<BlockDevSignalMask, BlockDevWakers>>,
    intc: Option<Arc<Gic>>,
    irq_line: u32,
    target_vcpu: u64,

    mem: GuestMemory,
    disk: Arc<DiskProperties>,
}

#[repr(C)]
#[derive(Debug, Copy, Clone, PartialEq, Default, Pod, Zeroable)]
pub struct VirtioBlkDiscardWriteZeroes {
    pub sector: __le64,
    pub num_sectors: __le32,
    pub flags: __le32,
}

impl BlockWorker {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        queue: Queue,
        signals: Arc<SignalChannel<BlockDevSignalMask, BlockDevWakers>>,
        intc: Option<Arc<Gic>>,
        irq_line: u32,
        target_vcpu: u64,
        mem: GuestMemory,
        disk: Arc<DiskProperties>,
    ) -> Self {
        Self {
            queue,
            signals,
            intc,
            irq_line,
            target_vcpu,

            mem,
            disk,
        }
    }

    pub fn run(self) -> thread::JoinHandle<()> {
        thread::Builder::new()
            .name(format!("block worker {}", self.target_vcpu))
            .spawn(|| {
                // worker is only used for slower requests, e.g. fsync, which waits on disk I/O
                utils::qos::set_thread_qos(QosClass::Utility, None).unwrap();

                self.work()
            })
            .unwrap()
    }

    fn work(mut self) {
        // Async BlockWorkers imply the use of only a single worker queue
        let mask = BlockDevSignalMask::REQ | BlockDevSignalMask::STOP_WORKER;

        loop {
            self.signals.wait_on_park(mask);

            let taken = self.signals.take(mask);

            if taken.intersects(BlockDevSignalMask::REQ) {
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

    fn process_queue(&mut self, mem: &GuestMemory) {
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
                let data_len = writer.available_bytes() as u64 - 1;
                if data_len % SECTOR_SIZE != 0 {
                    Err(RequestError::InvalidDataLength)
                } else {
                    let mut off = request_header.sector * SECTOR_SIZE;
                    writer
                        .for_each_iovec(data_len as usize, |iov| {
                            let n = self.disk.read_to_iovec(off as usize, iov)?;
                            off += n as u64;
                            Ok(())
                        })
                        .map_err(RequestError::WritingToDescriptor)?;
                    Ok(data_len as usize)
                }
            }
            VIRTIO_BLK_T_OUT => {
                let data_len = reader.available_bytes() as u64;
                if data_len % SECTOR_SIZE != 0 {
                    Err(RequestError::InvalidDataLength)
                } else {
                    let offset = request_header.sector * SECTOR_SIZE;
                    reader
                        .consume(data_len as usize, |bufs| {
                            self.disk.write_iovecs(offset, bufs)
                        })
                        .map_err(RequestError::ReadingFromDescriptor)
                }
            }
            VIRTIO_BLK_T_DISCARD | VIRTIO_BLK_T_WRITE_ZEROES => {
                while reader.available_bytes() >= size_of::<VirtioBlkDiscardWriteZeroes>() {
                    let seg: VirtioBlkDiscardWriteZeroes = reader
                        .read_obj()
                        .map_err(RequestError::ReadingFromDescriptor)?;

                    let offset = seg.sector * SECTOR_SIZE;
                    let len = seg.num_sectors as u64 * SECTOR_SIZE;

                    match self.disk.punch_hole(offset, len) {
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
            VIRTIO_BLK_T_FLUSH => {
                self.disk.flush().map_err(RequestError::FlushingToDisk)?;
                Ok(0)
            }
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
        if let Some(intc) = &self.intc {
            intc.set_irq_for_vcpu(Some(self.target_vcpu), self.irq_line);
        }
    }
}
