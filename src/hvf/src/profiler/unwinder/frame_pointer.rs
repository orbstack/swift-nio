use crate::profiler::memory::{is_valid_address, read_host_mem_aligned, PAC_MASK};

use super::{UnwindRegs, Unwinder, STACK_DEPTH_LIMIT};

pub struct FramePointerUnwinder {}

impl Unwinder for FramePointerUnwinder {
    fn unwind(&mut self, regs: UnwindRegs, mut f: impl FnMut(u64)) -> super::Result<()> {
        // start with just PC and LR
        f(regs.pc);

        // LR may be loaded from stack, so strip PAC signature
        let initial_lr = regs.lr & PAC_MASK;
        // validate address: LR could be used as a scratch register in the middle of the function
        if is_valid_address(initial_lr) {
            // TODO: subtract 1 for lookup?
            f(initial_lr);
        }

        // then start looking at FP
        let mut fp = regs.fp;
        // subtract 2 for first two frames (PC and LR)
        for i in 0..(STACK_DEPTH_LIMIT - 2) {
            if fp == 0 {
                // reached end of stack
                break;
            }

            // if bit 60 is set in FP, this is a swift async frame
            // but FP still points to the next FP, and AsyncContext is at FP-8, so we don't have to do anything except clearing the async bit to avoid triggering the high-bit bail out check below
            fp &= !(1 << 60);

            // mem[FP+8] = frame's LR
            let Some(mut frame_lr) = (unsafe { read_host_mem_aligned::<u64>(fp + 8) }) else {
                // invalid address
                break;
            };
            // strip PAC signature
            frame_lr &= PAC_MASK;
            if frame_lr == 0 {
                // reached end of stack
                break;
            }
            if !is_valid_address(frame_lr) {
                break;
            }

            if i == 0 && frame_lr == initial_lr {
                // skip duplicate LR if FP was already updated (i.e. not in prologue or epilogue)
            } else {
                // TODO: subtract 1 for lookup?
                f(frame_lr);
            }

            // mem[FP] = link to last FP
            let Some(next_fp) = (unsafe { read_host_mem_aligned::<u64>(fp) }) else {
                break;
            };
            fp = next_fp;
        }

        Ok(())
    }
}
