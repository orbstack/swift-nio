#![allow(deprecated)] // mach2 doesn't actually have the dyld functions

use std::{collections::HashMap, ffi::CStr};

use anyhow::anyhow;
use framehop::{
    aarch64::{CacheAarch64, PtrAuthMask, UnwindRegsAarch64, UnwinderAarch64},
    ExplicitModuleSectionInfo, Module, MustNotAllocateDuringUnwind, Unwinder as FUnwinder,
};
use goblin::mach::Mach;
use libc::{_dyld_get_image_header, _dyld_get_image_name, _dyld_image_count};
use mach2::{traps::mach_task_self, vm::mach_vm_read};
use tracing::info;

use crate::check_mach;

pub const STACK_DEPTH_LIMIT: usize = 128;

// no real address can be in __PAGEZERO (which is the full 32-bit space)
const MIN_ADDR: u64 = 0x100000000;

// mask out PAC signature, assuming 47-bit VA (machdep.virtual_address_size)
const PAC_MASK: u64 = u64::MAX >> 17;

#[derive(Debug, Copy, Clone)]
pub struct UnwindRegs {
    pub pc: u64,
    pub lr: u64,
    pub fp: u64,
    // used by DWARF CFI
    pub sp: u64,
}

pub trait Unwinder {
    fn unwind(&mut self, regs: UnwindRegs, f: impl FnMut(u64)) -> anyhow::Result<()>;
}

pub struct FramePointerUnwinder {}

impl Unwinder for FramePointerUnwinder {
    fn unwind(&mut self, regs: UnwindRegs, mut f: impl FnMut(u64)) -> anyhow::Result<()> {
        // start with just PC and LR
        f(regs.pc);

        // subtract 1 for lookup
        // TODO: this logic should probably be in symbolicator?
        let initial_lr = regs.lr & PAC_MASK;
        if initial_lr >= MIN_ADDR {
            f(initial_lr);
        }

        //println!("walking stack: PC={:x}, LR={:x}", regs.pc, regs.lr);
        // then start looking at FP
        let mut fp = regs.fp;
        // subtract 2 for first two frames (PC and LR)
        for i in 0..(STACK_DEPTH_LIMIT - 2) {
            // if bit 60 is set in FP, this is a swift async frame
            // but FP still points to the next FP, and AsyncContext is at FP-8, so we don't have to do anything except clearing the async bit to avoid triggering the high-bit bail out check below
            fp &= !(1 << 60);

            if fp == 0 {
                // reached end of stack
                break;
            }

            // avoid segfaulting:
            // high bits set = invalid address
            // TODO: handle Cgo stacks
            if fp & !PAC_MASK != 0 {
                break;
            }
            // below zero page = invalid address
            if fp < MIN_ADDR {
                break;
            }

            // mem[FP+8] = frame's LR
            //println!("walking stack: fp={:x}", fp);
            let frame_lr = unsafe { ((fp + 8) as *const u64).read() } & PAC_MASK;
            if frame_lr == 0 {
                // reached end of stack
                break;
            }
            // below zero page = invalid address
            if frame_lr < MIN_ADDR {
                break;
            }

            //println!("got LR: {:x}", frame_lr);
            // TODO: subtract LR
            if i == 0 && frame_lr == initial_lr {
                // skip duplicate LR if FP was already updated (i.e. not in prologue or epilogue)
            } else {
                f(frame_lr);
            }

            // mem[FP] = link to last FP
            fp = unsafe { (fp as *const u64).read() };
            //println!("got FP: {:x}", fp);
        }

        Ok(())
    }
}

pub struct FramehopUnwinder<'a> {
    cache: CacheAarch64<MustNotAllocateDuringUnwind>,
    unwinder: UnwinderAarch64<&'a [u8], MustNotAllocateDuringUnwind>,
}

impl FramehopUnwinder<'_> {
    pub fn new() -> anyhow::Result<Self> {
        let cache = CacheAarch64::<MustNotAllocateDuringUnwind>::new_in();
        let mut unwinder = UnwinderAarch64::new();

        let count = unsafe { _dyld_image_count() };
        for i in 0..count {
            let name_ptr = unsafe { _dyld_get_image_name(i) };
            if name_ptr.is_null() {
                // image_index out of bounds - an image was unloaded
                continue;
            }

            let header = unsafe { _dyld_get_image_header(i) };
            if header.is_null() {
                // image_index out of bounds - an image was unloaded
                continue;
            }

            // we don't actually know the size yet...
            let header_slice =
                unsafe { std::slice::from_raw_parts(header as *const u8, isize::MAX as usize) };
            let macho = match Mach::parse(header_slice)? {
                Mach::Binary(b) => b,
                Mach::Fat(_) => return Err(anyhow!("unexpected fat binary in memory")),
            };

            // __TEXT segment includes Mach-O header
            let text_seg = macho
                .segments
                .iter()
                .find(|seg| seg.name().is_ok_and(|s| s == "__TEXT"))
                .ok_or_else(|| anyhow!("no __TEXT segment"))?;

            // all sections we care about in the __TEXT segment
            // note: __text is a section in the __TEXT segment
            let mut sections = HashMap::new();
            for res in text_seg.into_iter() {
                let (section, data) = res?;
                sections.insert(
                    section.name()?.to_string(),
                    (section.addr, section.size, data),
                );
            }

            let get_section_svma = |section_name: &str| {
                sections
                    .get(section_name)
                    .map(|&(addr, size, _)| addr..(addr + size))
            };
            let get_section_data =
                |section_name: &str| sections.get(section_name).map(|&(_, _, data)| data);

            let name = unsafe { CStr::from_ptr(name_ptr) }
                .to_string_lossy()
                .to_string();
            let base_avma = header as u64;
            let avma_range = base_avma..(base_avma + text_seg.vmsize);
            info!(
                "adding module '{name}' at {:#x}-{:#x}",
                avma_range.start, avma_range.end
            );

            if name.contains("Hypervisor") {
                for item in macho.symbols() {
                    let (name, nlist) = item?;
                    info!(name, ?nlist, "HV symbol");
                }
            }

            let module = Module::new(
                name,
                avma_range,
                base_avma,
                ExplicitModuleSectionInfo {
                    base_svma: text_seg.vmaddr,
                    text_svma: get_section_svma("__text"),
                    text: get_section_data("__text"),
                    stubs_svma: get_section_svma("__stubs"),
                    stub_helper_svma: get_section_svma("__stub_helper"),
                    got_svma: get_section_svma("__got"),
                    unwind_info: get_section_data("__unwind_info"),
                    eh_frame_svma: get_section_svma("__eh_frame"),
                    eh_frame: get_section_data("__eh_frame"),
                    eh_frame_hdr_svma: get_section_svma("__eh_frame_hdr"),
                    eh_frame_hdr: get_section_data("__eh_frame_hdr"),
                    debug_frame: get_section_data("__debug_frame"),
                    text_segment_svma: Some(text_seg.vmaddr..(text_seg.vmaddr + text_seg.vmsize)),
                    text_segment: Some(text_seg.data),
                },
            );
            unwinder.add_module(module);
        }

        Ok(Self { cache, unwinder })
    }
}

impl Unwinder for FramehopUnwinder<'_> {
    fn unwind(&mut self, regs: UnwindRegs, mut f: impl FnMut(u64)) -> anyhow::Result<()> {
        let mut read_stack = |addr: u64| {
            let mut ptr: usize = 0;
            let mut ptr_size = 8;
            unsafe {
                if let Ok(()) = check_mach!(mach_vm_read(
                    mach_task_self(),
                    addr,
                    8,
                    &mut ptr,
                    &mut ptr_size,
                )) {
                    Ok((ptr as *const u64).read())
                } else {
                    Err(())
                }
            }
        };

        let mask = PtrAuthMask::from_max_known_address(PAC_MASK);
        let mut iter = self.unwinder.iter_frames(
            mask.strip_ptr_auth(regs.pc),
            UnwindRegsAarch64::new_with_ptr_auth_mask(mask, regs.lr, regs.sp, regs.fp),
            &mut self.cache,
            &mut read_stack,
        );
        while let Ok(Some(frame)) = iter.next() {
            f(frame.address_for_lookup());
        }
        Ok(())
    }
}
