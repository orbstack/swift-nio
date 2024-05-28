// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

use std::cell::Cell;
use std::cmp;
use std::fmt::{self, Display};
use std::fs::File;
use std::io::{self, IoSlice, IoSliceMut, Read, Write};
use std::marker::PhantomData;
use std::mem::{size_of, MaybeUninit};
use std::ops::Deref;
use std::os::fd::AsRawFd;
use std::ptr::copy_nonoverlapping;
use std::result;

use crate::virtio::queue::DescriptorChain;
use libc::c_void;
use nix::sys::uio::{preadv, pwritev, readv, writev};
use smallvec::SmallVec;
use vm_memory::{
    Address, ByteValued, Bytes, GuestAddress, GuestMemory, GuestMemoryError, GuestMemoryMmap,
    GuestMemoryRegion, Le16, Le32, Le64, VolatileMemory, VolatileMemoryError, VolatileSlice,
};

const INLINE_IOVECS: usize = 16;

#[derive(Debug)]
pub enum Error {
    DescriptorChainOverflow,
    FindMemoryRegion,
    GuestMemoryError(GuestMemoryError),
    InvalidChain,
    IoError(io::Error),
    SplitOutOfBounds(usize),
    VolatileMemoryError(VolatileMemoryError),
}

impl Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        use self::Error::*;

        match self {
            DescriptorChainOverflow => write!(
                f,
                "the combined length of all the buffers in a `DescriptorChain` would overflow"
            ),
            FindMemoryRegion => write!(f, "no memory region for this address range"),
            GuestMemoryError(e) => write!(f, "descriptor guest memory error: {e}"),
            InvalidChain => write!(f, "invalid descriptor chain"),
            IoError(e) => write!(f, "descriptor I/O error: {e}"),
            SplitOutOfBounds(off) => write!(f, "`DescriptorChain` split is out of bounds: {off}"),
            VolatileMemoryError(e) => write!(f, "volatile memory error: {e}"),
        }
    }
}

pub type Result<T> = result::Result<T, Error>;

impl std::error::Error for Error {}

#[repr(transparent)]
#[derive(Clone)]
pub struct Iovec<'a> {
    iov: libc::iovec,
    _phantom: PhantomData<&'a ()>,
}

unsafe impl<'a> Send for Iovec<'a> {}
unsafe impl<'a> Sync for Iovec<'a> {}

impl<'a> Iovec<'a> {
    pub fn len(&self) -> usize {
        self.iov.iov_len
    }

    pub fn set_len(&mut self, len: usize) {
        self.iov.iov_len = len;
    }

    pub fn addr(&self) -> *const u8 {
        self.iov.iov_base as *const u8
    }

    pub fn addr_mut(&self) -> *mut u8 {
        self.iov.iov_base as *mut u8
    }

    pub fn advance(&mut self, len: usize) {
        self.iov.iov_base = unsafe { self.iov.iov_base.offset(len as isize) } as *mut c_void;
        self.iov.iov_len -= len;
    }

    pub fn slice_to_std(iovs: &'a [Iovec<'a>]) -> &'a [IoSlice<'a>] {
        // safe: std IoSlice is guaranteed to be ABI compatible with iovec
        unsafe { std::slice::from_raw_parts(iovs.as_ptr() as *const IoSlice<'a>, iovs.len()) }
    }

    pub fn slice_to_std_mut(iovs: &'a [Iovec<'a>]) -> &'a mut [IoSliceMut<'a>] {
        // UNSAFE! but nix requires &mut [IoSliceMut] and doesn't actually mutate it, so it's ok...
        unsafe {
            std::slice::from_raw_parts_mut(
                iovs.as_ptr() as *const IoSliceMut<'a> as *mut IoSliceMut<'a>,
                iovs.len(),
            )
        }
    }
}

impl<'a> From<VolatileSlice<'a>> for Iovec<'a> {
    fn from(slice: VolatileSlice<'a>) -> Self {
        Iovec {
            iov: libc::iovec {
                iov_base: slice.ptr_guard_mut().as_ptr() as *mut c_void,
                iov_len: slice.len(),
            },
            _phantom: PhantomData,
        }
    }
}

struct DescriptorChainConsumer<'a> {
    _buffers_vec: Cell<SmallVec<[Iovec<'a>; INLINE_IOVECS]>>,
    buffers_pos: usize,
    bytes_consumed: usize,
}

impl<'a> DescriptorChainConsumer<'a> {
    pub fn new(buffers: SmallVec<[Iovec<'a>; INLINE_IOVECS]>) -> Self {
        DescriptorChainConsumer {
            _buffers_vec: Cell::new(buffers),
            buffers_pos: 0,
            bytes_consumed: 0,
        }
    }

    fn available_bytes(&mut self) -> usize {
        // This is guaranteed not to overflow because the total length of the chain
        // is checked during all creations of `DescriptorChainConsumer` (see
        // `Reader::new()` and `Writer::new()`).
        self.buffers_mut()
            .iter()
            .fold(0usize, |count, vs| count + vs.len())
    }

    fn bytes_consumed(&self) -> usize {
        self.bytes_consumed
    }

    fn buffers_mut(&mut self) -> &mut [Iovec<'a>] {
        &mut self._buffers_vec.get_mut()[self.buffers_pos..]
    }

    fn advance_buffers(&mut self, n: usize) {
        // Number of buffers to remove.
        let mut remove = 0;
        // Remaining length before reaching n.
        let mut left = n;
        for buf in self.buffers_mut().iter() {
            if let Some(remainder) = left.checked_sub(buf.len()) {
                left = remainder;
                remove += 1;
            } else {
                break;
            }
        }

        self.buffers_pos += remove;
        let bufs = self.buffers_mut();
        if bufs.is_empty() {
            assert!(left == 0, "advancing io slices beyond their length");
        } else {
            bufs[0].advance(left);
        }
    }

    /// Consumes at most `count` bytes from the `DescriptorChain`. Callers must provide a function
    /// that takes a `&[VolatileSlice]` and returns the total number of bytes consumed. This
    /// function guarantees that the combined length of all the slices in the `&[VolatileSlice]` is
    /// less than or equal to `count`.
    ///
    /// # Errors
    ///
    /// If the provided function returns any error then no bytes are consumed from the buffer and
    /// the error is returned to the caller.
    fn consume<F>(&mut self, count: usize, f: F) -> io::Result<usize>
    where
        F: FnOnce(&[Iovec]) -> io::Result<usize>,
    {
        // how many buffers do we need?
        let bufs = self.buffers_mut();
        let mut last_slice_index: Option<usize> = None;
        let mut last_slice_len: usize = 0;
        let mut bufs_len = 0;
        for (i, slice) in bufs.iter_mut().enumerate() {
            if bufs_len + slice.len() >= count {
                last_slice_index = Some(i);
                // cut this last slice short so it's not larger than len
                last_slice_len = slice.len();
                slice.set_len(count - bufs_len);
                break;
            }

            bufs_len += slice.len();
        }

        let last_slice_index = last_slice_index.ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "not enough buffers in descriptor chain",
            )
        })?;

        let bytes_consumed = f(&bufs[..last_slice_index + 1])?;

        // restore last slice
        bufs[last_slice_index].set_len(last_slice_len);

        // advance buffers
        self.advance_buffers(bytes_consumed);

        // This can happen if a driver tricks a device into reading/writing more data than
        // fits in a `usize`.
        let total_bytes_consumed =
            self.bytes_consumed
                .checked_add(bytes_consumed)
                .ok_or_else(|| {
                    io::Error::new(io::ErrorKind::InvalidData, Error::DescriptorChainOverflow)
                })?;

        self.bytes_consumed = total_bytes_consumed;
        Ok(bytes_consumed)
    }

    fn split_at(&mut self, offset: usize) -> Result<DescriptorChainConsumer<'a>> {
        // we currently only support skipping the first buffer
        if self.buffers_pos != 0
            || self.bytes_consumed != 0
            || self._buffers_vec.get_mut().len() == 0
            || self._buffers_vec.get_mut()[0].len() != offset
        {
            return Err(Error::SplitOutOfBounds(offset));
        }

        // self will be left with a len-1 slice, and we'll allocate once for self
        let mut other_bufs = SmallVec::<[Iovec<'a>; INLINE_IOVECS]>::with_capacity(1);
        other_bufs.push(self.buffers_mut()[0].clone());
        let other = DescriptorChainConsumer {
            _buffers_vec: Cell::new(other_bufs),
            buffers_pos: 1,
            bytes_consumed: 0,
        };

        self._buffers_vec.swap(&other._buffers_vec);
        Ok(other)
    }
}

#[repr(C)]
#[derive(Debug, Copy, Clone)]
pub struct GuestIoSlice {
    addr: GuestAddress,
    len: u64,
}

impl GuestIoSlice {
    pub fn new(addr: GuestAddress, len: usize) -> GuestIoSlice {
        GuestIoSlice {
            addr,
            len: len as u64,
        }
    }
}

/// Provides high-level interface over the sequence of memory regions
/// defined by readable descriptors in the descriptor chain.
///
/// Note that virtio spec requires driver to place any device-writable
/// descriptors after any device-readable descriptors (2.6.4.2 in Virtio Spec v1.1).
/// Reader will skip iterating over descriptor chain when first writable
/// descriptor is encountered.
pub struct Reader<'a> {
    buffer: DescriptorChainConsumer<'a>,
}

impl<'a> Reader<'a> {
    /// Construct a new Reader wrapper over `desc_chain`.
    pub fn new(mem: &'a GuestMemoryMmap, chain: DescriptorChain<'a>) -> Result<Reader<'a>> {
        let mut total_len: usize = 0;
        let buffers = chain
            .into_iter()
            .readable()
            .map(|desc| {
                // Verify that summing the descriptor sizes does not overflow.
                // This can happen if a driver tricks a device into reading more data than
                // fits in a `usize`.
                total_len = total_len
                    .checked_add(desc.len as usize)
                    .ok_or(Error::DescriptorChainOverflow)?;

                let region = mem.find_region(desc.addr).ok_or(Error::FindMemoryRegion)?;
                let offset = desc
                    .addr
                    .checked_sub(region.start_addr().raw_value())
                    .unwrap();
                region
                    .deref()
                    .get_slice(offset.raw_value() as usize, desc.len as usize)
                    .map_err(Error::VolatileMemoryError)
                    .map(|vs| vs.into())
            })
            .collect::<Result<SmallVec<[Iovec<'a>; INLINE_IOVECS]>>>()?;
        Ok(Reader {
            buffer: DescriptorChainConsumer::new(buffers),
        })
    }

    pub fn new_from_slices(
        mem: &'a GuestMemoryMmap,
        slices1: &[GuestIoSlice],
        slices2: &[GuestIoSlice],
    ) -> Result<Reader<'a>> {
        let mut total_len: usize = 0;
        let buffers = slices1
            .iter()
            .chain(slices2.iter())
            .map(|s| {
                // Verify that summing the slice sizes does not overflow.
                // This can happen if a driver tricks a device into reading more data than
                // fits in a `usize`.
                total_len = total_len
                    .checked_add(s.len as usize)
                    .ok_or(Error::DescriptorChainOverflow)?;

                let region = mem.find_region(s.addr).ok_or(Error::FindMemoryRegion)?;
                let offset = s.addr.checked_sub(region.start_addr().raw_value()).unwrap();
                region
                    .deref()
                    .get_slice(offset.raw_value() as usize, s.len as usize)
                    .map_err(Error::VolatileMemoryError)
                    .map(|vs| vs.into())
            })
            .collect::<Result<SmallVec<[Iovec<'a>; INLINE_IOVECS]>>>()?;
        Ok(Reader {
            buffer: DescriptorChainConsumer::new(buffers),
        })
    }

    /// Reads an object from the descriptor chain buffer.
    pub fn read_obj<T: ByteValued>(&mut self) -> io::Result<T> {
        let mut obj = MaybeUninit::<T>::uninit();

        // Safe because `MaybeUninit` guarantees that the pointer is valid for
        // `size_of::<T>()` bytes.
        let buf = unsafe {
            ::std::slice::from_raw_parts_mut(obj.as_mut_ptr() as *mut u8, size_of::<T>())
        };

        self.read_exact(buf)?;

        // Safe because any type that implements `ByteValued` can be considered initialized
        // even if it is filled with random data.
        Ok(unsafe { obj.assume_init() })
    }

    /// Reads data from the descriptor chain buffer into a file descriptor.
    /// Returns the number of bytes read from the descriptor chain buffer.
    /// The number of bytes read can be less than `count` if there isn't
    /// enough data in the descriptor chain buffer.
    pub fn read_to(&mut self, dst: &mut File, count: usize) -> io::Result<usize> {
        self.buffer.consume(count, |bufs| {
            writev(dst.as_raw_fd(), Iovec::slice_to_std(bufs)).map_err(|e| e.into())
        })
    }

    /// Reads data from the descriptor chain buffer into a File at offset `off`.
    /// Returns the number of bytes read from the descriptor chain buffer.
    /// The number of bytes read can be less than `count` if there isn't
    /// enough data in the descriptor chain buffer.
    pub fn read_to_at(&mut self, dst: &File, count: usize, off: u64) -> io::Result<usize> {
        self.buffer.consume(count, |bufs| {
            pwritev(dst.as_raw_fd(), Iovec::slice_to_std(bufs), off as i64).map_err(|e| e.into())
        })
    }

    pub fn read_exact_to(&mut self, dst: &mut File, mut count: usize) -> io::Result<()> {
        while count > 0 {
            match self.read_to(dst, count) {
                Ok(0) => {
                    return Err(io::Error::new(
                        io::ErrorKind::UnexpectedEof,
                        "failed to fill whole buffer",
                    ))
                }
                Ok(n) => count -= n,
                Err(ref e) if e.kind() == io::ErrorKind::Interrupted => {}
                Err(e) => return Err(e),
            }
        }

        Ok(())
    }

    /// Returns number of bytes available for reading.  May return an error if the combined
    /// lengths of all the buffers in the DescriptorChain would cause an integer overflow.
    pub fn available_bytes(&mut self) -> usize {
        self.buffer.available_bytes()
    }

    /// Returns number of bytes already read from the descriptor chain buffer.
    pub fn bytes_read(&self) -> usize {
        self.buffer.bytes_consumed()
    }

    /// Splits this `Reader` into two at the given offset in the `DescriptorChain` buffer.
    /// After the split, `self` will be able to read up to `offset` bytes while the returned
    /// `Reader` can read up to `available_bytes() - offset` bytes.  Returns an error if
    /// `offset > self.available_bytes()`.
    pub fn split_at(&mut self, offset: usize) -> Result<Reader<'a>> {
        self.buffer.split_at(offset).map(|buffer| Reader { buffer })
    }
}

impl<'a> io::Read for Reader<'a> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        self.buffer.consume(buf.len(), |bufs| {
            let mut rem = buf;
            let mut total = 0;
            for iov in bufs {
                let copy_len = cmp::min(rem.len(), iov.len());

                // Safe because we have already verified that `vs` points to valid memory.
                unsafe {
                    copy_nonoverlapping(iov.addr(), rem.as_mut_ptr(), copy_len);
                }
                rem = &mut rem[copy_len..];
                total += copy_len;
            }
            Ok(total)
        })
    }
}

/// Provides high-level interface over the sequence of memory regions
/// defined by writable descriptors in the descriptor chain.
///
/// Note that virtio spec requires driver to place any device-writable
/// descriptors after any device-readable descriptors (2.6.4.2 in Virtio Spec v1.1).
/// Writer will start iterating the descriptors from the first writable one and will
/// assume that all following descriptors are writable.
pub struct Writer<'a> {
    buffer: DescriptorChainConsumer<'a>,
}

impl<'a> Writer<'a> {
    /// Construct a new Writer wrapper over `desc_chain`.
    pub fn new(mem: &'a GuestMemoryMmap, chain: DescriptorChain<'a>) -> Result<Writer<'a>> {
        let mut total_len: usize = 0;
        let buffers = chain
            .into_iter()
            .writable()
            .map(|desc| {
                // Verify that summing the descriptor sizes does not overflow.
                // This can happen if a driver tricks a device into writing more data than
                // fits in a `usize`.
                total_len = total_len
                    .checked_add(desc.len as usize)
                    .ok_or(Error::DescriptorChainOverflow)?;

                let region = mem.find_region(desc.addr).ok_or(Error::FindMemoryRegion)?;
                let offset = desc
                    .addr
                    .checked_sub(region.start_addr().raw_value())
                    .unwrap();
                region
                    .deref()
                    .get_slice(offset.raw_value() as usize, desc.len as usize)
                    .map_err(Error::VolatileMemoryError)
                    .map(|vs: VolatileSlice<()>| vs.into())
            })
            .collect::<Result<SmallVec<[Iovec<'a>; INLINE_IOVECS]>>>()?;

        Ok(Writer {
            buffer: DescriptorChainConsumer::new(buffers),
        })
    }

    pub fn new_from_slices(
        mem: &'a GuestMemoryMmap,
        slices1: &[GuestIoSlice],
        slices2: &[GuestIoSlice],
    ) -> Result<Writer<'a>> {
        let mut total_len: usize = 0;
        let buffers = slices1
            .iter()
            .chain(slices2.iter())
            .map(|s| {
                // Verify that summing the slice sizes does not overflow.
                // This can happen if a driver tricks a device into writing more data than
                // fits in a `usize`.
                total_len = total_len
                    .checked_add(s.len as usize)
                    .ok_or(Error::DescriptorChainOverflow)?;

                let region = mem.find_region(s.addr).ok_or(Error::FindMemoryRegion)?;
                let offset = s.addr.checked_sub(region.start_addr().raw_value()).unwrap();
                region
                    .deref()
                    .get_slice(offset.raw_value() as usize, s.len as usize)
                    .map_err(Error::VolatileMemoryError)
                    .map(|vs| vs.into())
            })
            .collect::<Result<SmallVec<[Iovec<'a>; INLINE_IOVECS]>>>()?;
        Ok(Writer {
            buffer: DescriptorChainConsumer::new(buffers),
        })
    }

    /// Writes an object to the descriptor chain buffer.
    pub fn write_obj<T: ByteValued>(&mut self, val: T) -> io::Result<()> {
        self.write_all(val.as_slice())
    }

    /// Returns number of bytes available for writing.  May return an error if the combined
    /// lengths of all the buffers in the DescriptorChain would cause an overflow.
    pub fn available_bytes(&mut self) -> usize {
        self.buffer.available_bytes()
    }

    /// Writes data to the descriptor chain buffer from a file descriptor.
    /// Returns the number of bytes written to the descriptor chain buffer.
    /// The number of bytes written can be less than `count` if
    /// there isn't enough data in the descriptor chain buffer.
    pub fn write_from(&mut self, src: &mut File, count: usize) -> io::Result<usize> {
        self.buffer.consume(count, |bufs| {
            readv(src.as_raw_fd(), Iovec::slice_to_std_mut(bufs)).map_err(|e| e.into())
        })
    }

    /// Writes data to the descriptor chain buffer from a File at offset `off`.
    /// Returns the number of bytes written to the descriptor chain buffer.
    /// The number of bytes written can be less than `count` if
    /// there isn't enough data in the descriptor chain buffer.
    pub fn write_from_at(&mut self, src: &File, count: usize, off: u64) -> io::Result<usize> {
        self.buffer.consume(count, |bufs| {
            preadv(src.as_raw_fd(), Iovec::slice_to_std_mut(bufs), off as i64).map_err(|e| e.into())
        })
    }

    pub fn write_all_from(&mut self, src: &mut File, mut count: usize) -> io::Result<()> {
        while count > 0 {
            match self.write_from(src, count) {
                Ok(0) => {
                    return Err(io::Error::new(
                        io::ErrorKind::WriteZero,
                        "failed to write whole buffer",
                    ))
                }
                Ok(n) => count -= n,
                Err(ref e) if e.kind() == io::ErrorKind::Interrupted => {}
                Err(e) => return Err(e),
            }
        }

        Ok(())
    }

    /// Returns number of bytes already written to the descriptor chain buffer.
    pub fn bytes_written(&self) -> usize {
        self.buffer.bytes_consumed()
    }

    /// Splits this `Writer` into two at the given offset in the `DescriptorChain` buffer.
    /// After the split, `self` will be able to write up to `offset` bytes while the returned
    /// `Writer` can write up to `available_bytes() - offset` bytes.  Returns an error if
    /// `offset > self.available_bytes()`.
    pub fn split_at(&mut self, offset: usize) -> Result<Writer<'a>> {
        self.buffer.split_at(offset).map(|buffer| Writer { buffer })
    }
}

impl<'a> io::Write for Writer<'a> {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.buffer.consume(buf.len(), |bufs| {
            let mut rem = buf;
            let mut total = 0;
            for iov in bufs {
                let copy_len = cmp::min(rem.len(), iov.len());

                // Safe because we have already verified that `vs` points to valid memory.
                unsafe {
                    copy_nonoverlapping(rem.as_ptr(), iov.addr_mut(), copy_len);
                }
                rem = &rem[copy_len..];
                total += copy_len;
            }
            Ok(total)
        })
    }

    fn flush(&mut self) -> io::Result<()> {
        // Nothing to flush since the writes go straight into the buffer.
        Ok(())
    }
}

const VIRTQ_DESC_F_NEXT: u16 = 0x1;
const VIRTQ_DESC_F_WRITE: u16 = 0x2;

#[derive(Copy, Clone, PartialEq, Eq)]
pub enum DescriptorType {
    Readable,
    Writable,
}

#[derive(Copy, Clone, Debug, Default)]
#[repr(C)]
struct virtq_desc {
    addr: Le64,
    len: Le32,
    flags: Le16,
    next: Le16,
}

// Safe because it only has data and has no implicit padding.
unsafe impl ByteValued for virtq_desc {}

/// Test utility function to create a descriptor chain in guest memory.
pub fn create_descriptor_chain(
    memory: &GuestMemoryMmap,
    descriptor_array_addr: GuestAddress,
    mut buffers_start_addr: GuestAddress,
    descriptors: Vec<(DescriptorType, u32)>,
    spaces_between_regions: u32,
) -> Result<DescriptorChain> {
    let descriptors_len = descriptors.len();
    for (index, (type_, size)) in descriptors.into_iter().enumerate() {
        let mut flags = 0;
        if let DescriptorType::Writable = type_ {
            flags |= VIRTQ_DESC_F_WRITE;
        }
        if index + 1 < descriptors_len {
            flags |= VIRTQ_DESC_F_NEXT;
        }

        let index = index as u16;
        let desc = virtq_desc {
            addr: buffers_start_addr.raw_value().into(),
            len: size.into(),
            flags: flags.into(),
            next: (index + 1).into(),
        };

        let offset = size + spaces_between_regions;
        buffers_start_addr = buffers_start_addr
            .checked_add(u64::from(offset))
            .ok_or(Error::InvalidChain)?;

        let _ = memory.write_obj(
            desc,
            descriptor_array_addr
                .checked_add(u64::from(index) * std::mem::size_of::<virtq_desc>() as u64)
                .ok_or(Error::InvalidChain)?,
        );
    }

    DescriptorChain::checked_new(memory, descriptor_array_addr, 0x100, 0).ok_or(Error::InvalidChain)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reader_test_simple_chain() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 8),
                (Readable, 16),
                (Readable, 18),
                (Readable, 64),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");
        assert_eq!(reader.available_bytes(), 106);
        assert_eq!(reader.bytes_read(), 0);

        let mut buffer = [0_u8; 64];
        if let Err(_) = reader.read_exact(&mut buffer) {
            panic!("read_exact should not fail here");
        }

        assert_eq!(reader.available_bytes(), 42);
        assert_eq!(reader.bytes_read(), 64);

        match reader.read(&mut buffer) {
            Err(_) => panic!("read should not fail here"),
            Ok(length) => assert_eq!(length, 42),
        }

        assert_eq!(reader.available_bytes(), 0);
        assert_eq!(reader.bytes_read(), 106);
    }

    #[test]
    fn writer_test_simple_chain() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Writable, 8),
                (Writable, 16),
                (Writable, 18),
                (Writable, 64),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut writer = Writer::new(&memory, chain).expect("failed to create Writer");
        assert_eq!(writer.available_bytes(), 106);
        assert_eq!(writer.bytes_written(), 0);

        let mut buffer = [0_u8; 64];
        if let Err(_) = writer.write_all(&mut buffer) {
            panic!("write_all should not fail here");
        }

        assert_eq!(writer.available_bytes(), 42);
        assert_eq!(writer.bytes_written(), 64);

        match writer.write(&mut buffer) {
            Err(_) => panic!("write should not fail here"),
            Ok(length) => assert_eq!(length, 42),
        }

        assert_eq!(writer.available_bytes(), 0);
        assert_eq!(writer.bytes_written(), 106);
    }

    #[test]
    fn reader_test_incompatible_chain() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Writable, 8)],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");
        assert_eq!(reader.available_bytes(), 0);
        assert_eq!(reader.bytes_read(), 0);

        assert!(reader.read_obj::<u8>().is_err());

        assert_eq!(reader.available_bytes(), 0);
        assert_eq!(reader.bytes_read(), 0);
    }

    #[test]
    fn writer_test_incompatible_chain() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Readable, 8)],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut writer = Writer::new(&memory, chain).expect("failed to create Writer");
        assert_eq!(writer.available_bytes(), 0);
        assert_eq!(writer.bytes_written(), 0);

        assert!(writer.write_obj(0u8).is_err());

        assert_eq!(writer.available_bytes(), 0);
        assert_eq!(writer.bytes_written(), 0);
    }

    #[test]
    fn reader_writer_shared_chain() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 16),
                (Readable, 16),
                (Readable, 96),
                (Writable, 64),
                (Writable, 1),
                (Writable, 3),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain.clone()).expect("failed to create Reader");
        let mut writer = Writer::new(&memory, chain).expect("failed to create Writer");

        assert_eq!(reader.bytes_read(), 0);
        assert_eq!(writer.bytes_written(), 0);

        let mut buffer = Vec::with_capacity(200);

        assert_eq!(
            reader
                .read_to_end(&mut buffer)
                .expect("read should not fail here"),
            128
        );

        // The writable descriptors are only 68 bytes long.
        writer
            .write_all(&buffer[..68])
            .expect("write should not fail here");

        assert_eq!(reader.available_bytes(), 0);
        assert_eq!(reader.bytes_read(), 128);
        assert_eq!(writer.available_bytes(), 0);
        assert_eq!(writer.bytes_written(), 68);
    }

    #[test]
    fn reader_writer_shattered_object() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let secret: Le32 = 0x12345678.into();

        // Create a descriptor chain with memory regions that are properly separated.
        let chain_writer = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Writable, 1), (Writable, 1), (Writable, 1), (Writable, 1)],
            123,
        )
        .expect("create_descriptor_chain failed");
        let mut writer = Writer::new(&memory, chain_writer).expect("failed to create Writer");
        if let Err(_) = writer.write_obj(secret) {
            panic!("write_obj should not fail here");
        }

        // Now create new descriptor chain pointing to the same memory and try to read it.
        let chain_reader = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Readable, 1), (Readable, 1), (Readable, 1), (Readable, 1)],
            123,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain_reader).expect("failed to create Reader");
        match reader.read_obj::<Le32>() {
            Err(_) => panic!("read_obj should not fail here"),
            Ok(read_secret) => assert_eq!(read_secret, secret),
        }
    }

    #[test]
    fn reader_unexpected_eof() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Readable, 256), (Readable, 256)],
            0,
        )
        .expect("create_descriptor_chain failed");

        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        let mut buf = Vec::with_capacity(1024);
        buf.resize(1024, 0);

        assert_eq!(
            reader
                .read_exact(&mut buf[..])
                .expect_err("read more bytes than available")
                .kind(),
            io::ErrorKind::UnexpectedEof
        );
    }

    #[test]
    fn split_border() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 16),
                (Readable, 16),
                (Readable, 96),
                (Writable, 64),
                (Writable, 1),
                (Writable, 3),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        let other = reader.split_at(32).expect("failed to split Reader");
        assert_eq!(reader.available_bytes(), 32);
        assert_eq!(other.available_bytes(), 96);
    }

    #[test]
    fn split_middle() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 16),
                (Readable, 16),
                (Readable, 96),
                (Writable, 64),
                (Writable, 1),
                (Writable, 3),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        let other = reader.split_at(24).expect("failed to split Reader");
        assert_eq!(reader.available_bytes(), 24);
        assert_eq!(other.available_bytes(), 104);
    }

    #[test]
    fn split_end() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 16),
                (Readable, 16),
                (Readable, 96),
                (Writable, 64),
                (Writable, 1),
                (Writable, 3),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        let other = reader.split_at(128).expect("failed to split Reader");
        assert_eq!(reader.available_bytes(), 128);
        assert_eq!(other.available_bytes(), 0);
    }

    #[test]
    fn split_beginning() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 16),
                (Readable, 16),
                (Readable, 96),
                (Writable, 64),
                (Writable, 1),
                (Writable, 3),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        let other = reader.split_at(0).expect("failed to split Reader");
        assert_eq!(reader.available_bytes(), 0);
        assert_eq!(other.available_bytes(), 128);
    }

    #[test]
    fn split_outofbounds() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![
                (Readable, 16),
                (Readable, 16),
                (Readable, 96),
                (Writable, 64),
                (Writable, 1),
                (Writable, 3),
            ],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        if let Ok(_) = reader.split_at(256) {
            panic!("successfully split Reader with out of bounds offset");
        }
    }

    #[test]
    fn read_full() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Readable, 16), (Readable, 16), (Readable, 16)],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut reader = Reader::new(&memory, chain).expect("failed to create Reader");

        let mut buf = [0u8; 64];
        assert_eq!(
            reader.read(&mut buf[..]).expect("failed to read to buffer"),
            48
        );
    }

    #[test]
    fn write_full() {
        use DescriptorType::*;

        let memory_start_addr = GuestAddress(0x0);
        let memory = GuestMemoryMmap::from_ranges(&[(memory_start_addr, 0x10000)]).unwrap();

        let chain = create_descriptor_chain(
            &memory,
            GuestAddress(0x0),
            GuestAddress(0x100),
            vec![(Writable, 16), (Writable, 16), (Writable, 16)],
            0,
        )
        .expect("create_descriptor_chain failed");
        let mut writer = Writer::new(&memory, chain).expect("failed to create Writer");

        let buf = [0xdeu8; 64];
        assert_eq!(
            writer.write(&buf[..]).expect("failed to write from buffer"),
            48
        );
    }
}
