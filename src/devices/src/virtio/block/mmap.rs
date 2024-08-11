use std::{
    fs::File,
    io,
    os::fd::AsRawFd,
    sync::atomic::{AtomicU64, Ordering},
};

use mach2::{
    kern_return::KERN_SUCCESS,
    traps::mach_task_self,
    vm::mach_vm_map,
    vm_inherit::VM_INHERIT_NONE,
    vm_prot::VM_PROT_NONE,
    vm_statistics::VM_FLAGS_ANYWHERE,
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};

use crate::virtio::descriptor_utils::Iovec;

pub const CHUNK_SIZE: usize = 16 * 1024 * 1024 * 1024; // GiB

const VM_FLAGS_4GB_CHUNK: i32 = 4;

struct AtomicBitmap(Vec<AtomicU64>);

impl AtomicBitmap {
    pub fn new(num_bits: usize) -> Self {
        let num_chunks = num_bits.div_ceil(64);
        let mut v = Vec::with_capacity(num_chunks);
        v.resize_with(num_chunks, || AtomicU64::new(0));
        AtomicBitmap(v)
    }

    pub fn test(&self, bit: usize) -> bool {
        let chunk = bit / 64;
        let offset = bit % 64;
        self.0[chunk].load(Ordering::Relaxed) & (1 << offset) != 0
    }

    pub fn set(&self, bit: usize) {
        let chunk = bit / 64;
        let offset = bit % 64;
        self.0[chunk].fetch_or(1 << offset, Ordering::Relaxed);
    }
}

pub struct MappedFile {
    file: File,
    base_addr: *const u8,
    size: usize,
    mapped_bitmap: AtomicBitmap,
}

unsafe impl Send for MappedFile {}
unsafe impl Sync for MappedFile {}

impl MappedFile {
    pub fn map(file: File, size: usize) -> io::Result<Self> {
        // reserve contiguous address space for performance and to allow a compact bitmap
        // use 4G chunks to minimize regions
        let mut base_addr: mach_vm_address_t = 0;
        let ret = unsafe {
            mach_vm_map(
                mach_task_self(),
                &mut base_addr,
                size as mach_vm_size_t,
                0,
                VM_FLAGS_4GB_CHUNK | VM_FLAGS_ANYWHERE,
                0,
                0,
                0,
                // this mapping should never actually be used
                VM_PROT_NONE,
                VM_PROT_NONE,
                VM_INHERIT_NONE,
            )
        };
        if ret != KERN_SUCCESS {
            return Err(io::Error::new(
                io::ErrorKind::Other,
                format!("mach error: {}", ret),
            ));
        }

        let num_chunks = size.div_ceil(CHUNK_SIZE);

        Ok(MappedFile {
            file,
            base_addr: base_addr as *const u8,
            size,
            mapped_bitmap: AtomicBitmap::new(num_chunks),
        })
    }

    pub fn file(&self) -> &File {
        &self.file
    }

    pub fn read_to_iovec(&self, off: usize, iov: &Iovec) -> io::Result<usize> {
        // bounds check
        let len = iov.len();
        let src = self.get_host_addr(off, len)?;
        unsafe { std::ptr::copy_nonoverlapping(src, iov.addr_mut(), len) }
        Ok(len)
    }

    pub fn get_host_addr(&self, off: usize, len: usize) -> io::Result<*const u8> {
        // make sure all chunks included in this are mapped
        self.ensure_mapped(off, len)?;
        Ok(unsafe { self.base_addr.add(off) })
    }

    pub fn ensure_mapped(&self, off: usize, len: usize) -> io::Result<()> {
        // len=0 would cause the -1 below to overflow
        if len == 0 {
            return Ok(());
        }

        let end_off = off.saturating_add(len) - 1;
        if end_off > self.size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "read out of bounds",
            ));
        }

        let start_chunk = off / CHUNK_SIZE;
        let end_chunk = end_off / CHUNK_SIZE;

        for chunk in start_chunk..=end_chunk {
            // race OK, no need for synchronization:
            // mapping the same file+offset to the same address doesn't change anything
            if !self.mapped_bitmap.test(chunk) {
                let file_off = chunk * CHUNK_SIZE;
                let map_size = CHUNK_SIZE.min(self.size - file_off);
                let map_addr = unsafe { self.base_addr.add(chunk * CHUNK_SIZE) };
                let addr = unsafe {
                    libc::mmap(
                        map_addr as *mut _,
                        map_size,
                        libc::PROT_READ,
                        libc::MAP_FILE | libc::MAP_FIXED | libc::MAP_SHARED,
                        self.file.as_raw_fd(),
                        file_off as libc::off_t,
                    )
                };
                if addr == libc::MAP_FAILED {
                    return Err(io::Error::last_os_error());
                }

                self.mapped_bitmap.set(chunk);
            }
        }

        Ok(())
    }
}

impl Drop for MappedFile {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.base_addr as *mut libc::c_void, self.size);
        }
    }
}
