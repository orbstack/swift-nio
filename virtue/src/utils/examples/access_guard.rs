use std::{
    io,
    ptr::{null_mut, NonNull},
};

use libc::{MAP_ANONYMOUS, MAP_FAILED, MAP_PRIVATE, PROT_NONE};
use utils::memory::{GuestAddress, GuestMemory};

fn main() {
    let mem_size = 1024;
    let base = unsafe {
        libc::mmap(
            null_mut(),
            mem_size,
            PROT_NONE,
            MAP_ANONYMOUS | MAP_PRIVATE,
            -1,
            0,
        )
    };
    if base == MAP_FAILED {
        panic!("{}", io::Error::last_os_error());
    }

    let memory = unsafe {
        GuestMemory::new(
            NonNull::slice_from_raw_parts(NonNull::new_unchecked(base.cast()), mem_size),
            || {},
        )
    };

    memory.try_write(GuestAddress(0), &[0u32]).unwrap();
    dbg!(memory.try_read::<u64>(GuestAddress(0)).unwrap());
}
