use std::{cell::UnsafeCell, mem::MaybeUninit, ptr::NonNull};

use anyhow::anyhow;
use libc::{mmap, munmap};

pub struct BufferStack {
    start: *mut u8,
    next_pos: UnsafeCell<*mut u8>,
    end: *mut u8,
}

impl BufferStack {
    // in a 4096-char PATH_MAX, 2048 is the max possible depth: min 1 char per dir name + 1 char for '/'
    const MAX_DEPTH: usize = 2048;
    pub(crate) const BUF_SIZE: usize = 32768;

    pub fn new() -> anyhow::Result<Self> {
        let arena = unsafe {
            mmap(
                std::ptr::null_mut(),
                Self::BUF_SIZE * Self::MAX_DEPTH,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_PRIVATE | libc::MAP_ANONYMOUS,
                -1,
                0,
            )
        };
        if arena == libc::MAP_FAILED {
            return Err(anyhow!(
                "failed to allocate arena: {}",
                std::io::Error::last_os_error()
            ));
        }
        let arena = arena as *mut u8;

        Ok(Self {
            start: arena,
            next_pos: UnsafeCell::new(arena),
            end: unsafe { arena.add(Self::BUF_SIZE * Self::MAX_DEPTH) },
        })
    }

    pub fn next(&self) -> BufferStackGuard {
        let next_pos_ptr = self.next_pos.get();
        let cur_pos = unsafe { *next_pos_ptr };
        if cur_pos >= self.end {
            panic!(
                "too many nested directories: maximum depth is {}",
                Self::MAX_DEPTH
            );
        }

        unsafe { *next_pos_ptr = cur_pos.add(Self::BUF_SIZE) };
        BufferStackGuard {
            stack: self,
            pos: unsafe { NonNull::new_unchecked(cur_pos) },
        }
    }
}

impl Drop for BufferStack {
    fn drop(&mut self) {
        let ret = unsafe { munmap(self.start as *mut _, Self::BUF_SIZE * Self::MAX_DEPTH) };
        if ret == -1 {
            panic!(
                "failed to unmap buffer stack: {}",
                std::io::Error::last_os_error()
            );
        }
    }
}

pub struct BufferStackGuard<'a> {
    stack: &'a BufferStack,
    pos: NonNull<u8>,
}

impl BufferStackGuard<'_> {
    pub fn get(&mut self) -> &mut MaybeUninit<[u8; BufferStack::BUF_SIZE]> {
        unsafe {
            self.pos
                .cast::<MaybeUninit<[u8; BufferStack::BUF_SIZE]>>()
                .as_mut()
        }
    }
}

impl Drop for BufferStackGuard<'_> {
    fn drop(&mut self) {
        let next_pos_ptr = self.stack.next_pos.get();
        if unsafe { self.pos.as_ptr().add(BufferStack::BUF_SIZE) != *next_pos_ptr } {
            panic!("buffer stack guard dropped out of order");
        }

        unsafe { *next_pos_ptr = self.pos.as_ptr() };
    }
}
