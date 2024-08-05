use std::collections::VecDeque;

use crate::profiler::{symbolicator::SymbolResult, SymbolicatedFrame};

use super::StackTransform;

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
