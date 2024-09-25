use std::{collections::BTreeMap, ffi::c_void};

use nix::errno::Errno;
use tracing::debug_span;

use crate::memory::page_size;

bitflags::bitflags! {
    #[derive(Debug, PartialEq, PartialOrd, Eq, Ord)]
    struct MincoreFlags: u8 {
        const InCore = 0x1;
        const Ref = 0x2;
        const Modified = 0x4;
        const RefOther = 0x8;
        const ModifiedOther = 0x10;
        const PagedOut = 0x20;
        const Copied = 0x40;
        const Anon = 0x80;
    }
}

pub unsafe fn dump_mincore(host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
    let mut vec = vec![0u8; size / page_size()];
    let _span = debug_span!("dump_mincore", size = size / 1024).entered();
    let ret = libc::mincore(host_addr, size, vec.as_mut_ptr() as *mut _);
    Errno::result(ret)?;
    drop(_span);

    let mut buckets = BTreeMap::new();
    for &v in vec.iter() {
        let flags = MincoreFlags::from_bits_retain(v);
        *buckets.entry(flags).or_insert(0) += 1;
    }

    for (flags, count) in buckets {
        println!("    {:?}: {} KiB", flags, count * page_size() / 1024);
    }

    Ok(())
}
