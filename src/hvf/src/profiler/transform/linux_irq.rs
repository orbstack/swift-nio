use std::collections::VecDeque;

use crate::profiler::{symbolicator::SymbolResult, FrameCategory, SymbolicatedFrame};

use super::StackTransform;

pub struct LinuxIrqTransform {}

impl StackTransform for LinuxIrqTransform {
    fn transform(&self, stack: &mut VecDeque<SymbolicatedFrame>) -> anyhow::Result<()> {
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
