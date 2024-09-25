#![allow(deprecated)] // mach2 doesn't actually have the dyld functions

use std::{ffi::CStr, ops::Range};

use anyhow::anyhow;
use goblin::mach::Mach;
use libc::{
    _dyld_get_image_header, _dyld_get_image_name, _dyld_get_image_vmaddr_slide, _dyld_image_count,
};

pub struct LoadedImage {
    pub path: String,
    // header address + __TEXT segment's vmsize
    pub addr_range: Range<usize>,
    pub vmaddr_slide: isize,
}

pub fn get_loaded_images() -> anyhow::Result<Vec<LoadedImage>> {
    // this is only a hint;
    let count_hint = unsafe { _dyld_image_count() };
    let mut images = Vec::with_capacity(count_hint as usize);

    for i in 0.. {
        let name_ptr = unsafe { _dyld_get_image_name(i) };
        if name_ptr.is_null() {
            // image_index out of bounds; unloaded or end of list
            break;
        }

        let vmaddr_slide = unsafe { _dyld_get_image_vmaddr_slide(i) };
        if vmaddr_slide == 0 {
            // image_index out of bounds; unloaded or end of list
            break;
        }

        let header = unsafe { _dyld_get_image_header(i) };
        if header.is_null() {
            // image_index out of bounds; unloaded or end of list
            break;
        }

        // we don't actually know the size yet...
        let header_slice =
            unsafe { std::slice::from_raw_parts(header as *const u8, isize::MAX as usize) };
        let macho = match Mach::parse(header_slice)? {
            Mach::Binary(b) => b,
            // dyld should only be loading an architecture slice
            Mach::Fat(_) => return Err(anyhow!("unexpected fat binary in memory")),
        };

        // __TEXT segment includes Mach-O header
        let text_seg = macho
            .segments
            .iter()
            .find(|seg| seg.name().is_ok_and(|s| s == "__TEXT"))
            .ok_or_else(|| anyhow!("no __TEXT segment"))?;

        let name = unsafe { CStr::from_ptr(name_ptr) }
            .to_string_lossy()
            .to_string();
        let base_addr = header as usize;
        let addr_range = base_addr..(base_addr + text_seg.vmsize as usize);

        images.push(LoadedImage {
            path: name,
            addr_range,
            vmaddr_slide,
        });
    }

    Ok(images)
}
