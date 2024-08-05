use std::collections::VecDeque;

use crate::profiler::{symbolicator::SymbolResult, FrameCategory, SymbolicatedFrame};

use super::StackTransform;

pub struct CgoTransform {}

impl StackTransform for CgoTransform {
    fn transform(&self, stack: &mut VecDeque<SymbolicatedFrame>) -> anyhow::Result<()> {
        // remove everything before runtime.libcCall or runtime.asmcgocall.abi0, if it's present
        // we do need to keep runtime.libcCall to prevent partial stacks where it hasn't gotten to asmcgocall yet
        // it tends to be garbage, if it exists:
        /*
              Thread ''  (0x1403, 3143 samples)
        U 2206    runtime.asmcgocall.abi0+200  (OrbStack Helper)
          U 1715    runtime.syscall6.abi0+56  (OrbStack Helper)
            U 1715    kevent+8  (libsystem_kernel.dylib)
          U 491     runtime.pthread_cond_wait_trampoline.abi0+24  (OrbStack Helper)
            U 491     _pthread_cond_wait+1228  (libsystem_pthread.dylib)
              U 491     __psynch_cvwait+8  (libsystem_kernel.dylib)
        U 936     0x103fb4000041
          U 593     0x173862458
            U 593     runtime.libcCall+92  (OrbStack Helper)
              U 593     runtime.asmcgocall.abi0+200  (OrbStack Helper)
                U 593     runtime.kevent_trampoline.abi0+40  (OrbStack Helper)
                  U 593     kevent+8  (libsystem_kernel.dylib)
          U 343     0x173862418
            U 343     runtime.libcCall+92  (OrbStack Helper)
              U 343     runtime.asmcgocall.abi0+200  (OrbStack Helper)
                U 343     runtime.kevent_trampoline.abi0+40  (OrbStack Helper)
                  U 343     kevent+8  (libsystem_kernel.dylib)
               */
        for (i, sframe) in stack.iter().enumerate().rev() {
            // guest code can't run on Go threads
            if sframe.frame.category.is_guest() {
                break;
            }

            if let Some(SymbolResult {
                symbol_offset: Some((ref name, _)),
                ..
            }) = sframe.symbol
            {
                if (name == "runtime.libcCall" || name == "runtime.asmcgocall.abi0")
                    && i != stack.len() - 1
                {
                    stack.drain((i + 1)..);
                    break;
                }
            }
        }

        Ok(())
    }
}

pub struct LinuxIrqStackTransform {}

impl StackTransform for LinuxIrqStackTransform {
    fn transform(&self, stack: &mut VecDeque<SymbolicatedFrame>) -> anyhow::Result<()> {
        // do nothing if we're not in guest code
        // need to check before the loop because we do loop over both guest and host frames
        if let Some(sframe) = stack.front() {
            if !sframe.frame.category.is_guest() {
                return Ok(());
            }
        }

        // remove everything between hv_trap and "el1h_64_irq"
        // Linux does a good job of preserving FP on IRQ stack switch,
        // but it's really hard to read profiles when IRQs are all attached to random frames
        let mut irq_idx = None;
        for (i, sframe) in stack.iter().enumerate() {
            if let Some(SymbolResult {
                symbol_offset: Some((ref name, _)),
                ..
            }) = sframe.symbol
            {
                // once we get to el1h_64_irq, remember where it was
                if sframe.frame.category == FrameCategory::GuestKernel && name == "el1h_64_irq" {
                    // IRQs can be nested, and that's an equally confusing stack trace
                    // even though it's not the real stack, always graft the beginning of an IRQ handler onto hv_trap
                    // this removes all nested IRQs from the stack trace
                    if irq_idx.is_none() {
                        irq_idx = Some(i);
                    }
                }

                // once we get to hv_trap, remove everything between it and el1h_64_irq
                if sframe.frame.category == FrameCategory::HostUserspace && name == "hv_trap" {
                    if let Some(irq_idx) = irq_idx {
                        stack.drain((irq_idx + 1)..i);
                        break;
                    }
                }
            }
        }

        Ok(())
    }
}

pub struct LeafCallTransform {}

impl StackTransform for LeafCallTransform {
    fn transform(&self, stack: &mut VecDeque<SymbolicatedFrame>) -> anyhow::Result<()> {
        // if the last two frames (PC and LR) are in the same function, remove LR
        // this happens when a function calls leaf functions that don't save/restore LR from FP
        //
        // frame pointer-based unwinders don't usually run into this because they only use FP
        // and not LR, but we use LR to catch leaf calls, and then apply this fixup. it's not
        // really correct, but it lets us get by without looking up the PC in DWARF CFI/CUI to
        // figure out whether the LR is from a leaf call, and it isn't too bad in practice.
        // if we have a non-negligible amount of recursion, it'll be more than one frame

        if stack.len() < 2 {
            return Ok(());
        }

        let pc = &stack[0];
        let lr = &stack[1];
        if pc.frame.category != lr.frame.category {
            return Ok(());
        }

        if let Some(SymbolResult {
            image: ref pc_image,
            image_base: pc_base,
            symbol_offset: Some((ref pc_name, _)),
            ..
        }) = pc.symbol
        {
            if let Some(SymbolResult {
                image: ref lr_image,
                image_base: lr_base,
                symbol_offset: Some((ref lr_name, _)),
                ..
            }) = lr.symbol
            {
                if pc_image == lr_image && pc_base == lr_base && pc_name == lr_name {
                    // remove LR, not PC. PC is the code we're actually running now;
                    // LR should not be in the stack
                    stack.remove(1);
                }
            }
        }

        Ok(())
    }
}
