use std::{fs::File, io, os::fd::AsRawFd};

use crate::virtio::descriptor_utils::Iovec;

pub struct MappedFile {
    file: File,
    addr: *mut u8,
    size: usize,
}

unsafe impl Send for MappedFile {}
unsafe impl Sync for MappedFile {}

impl MappedFile {
    pub fn map(file: File, size: usize) -> io::Result<Self> {
        let addr = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                size,
                libc::PROT_READ,
                libc::MAP_SHARED,
                file.as_raw_fd(),
                0,
            )
        };
        if addr == libc::MAP_FAILED {
            return Err(io::Error::last_os_error());
        }

        Ok(MappedFile {
            file,
            addr: addr as *mut u8,
            size,
        })
    }

    pub fn file(&self) -> &File {
        &self.file
    }

    pub fn read_to_iovec(&self, off: usize, iov: &Iovec) -> io::Result<usize> {
        // bounds check
        let len = iov.len();
        if off.saturating_add(len) > self.size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "read out of bounds",
            ));
        }

        let src = unsafe { self.addr.add(off) };
        unsafe { std::ptr::copy_nonoverlapping(src, iov.addr_mut(), len) }
        Ok(len)
    }
}

impl Drop for MappedFile {
    fn drop(&mut self) {
        unsafe {
            libc::munmap(self.addr as *mut libc::c_void, self.size);
        }
    }
}
