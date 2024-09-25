use std::collections::VecDeque;

use crate::profiler::{
    symbolicator::{SymbolFunc, SymbolResult},
    FrameCategory, SymbolicatedFrame,
};

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

        // skip over host kernel frames. they're synthetic; the frames above them could have leaf call issues
        let start_i = stack
            .iter()
            .position(|sframe| sframe.frame.category == FrameCategory::HostKernel)
            .map(|i| i + 1)
            .unwrap_or(0);
        if start_i + 2 >= stack.len() {
            return Ok(());
        }

        let pc = &stack[start_i];
        let lr = &stack[start_i + 1];
        if pc.frame.category != lr.frame.category {
            return Ok(());
        }

        if let Some(SymbolResult {
            image: ref pc_image,
            image_base: pc_base,
            function: Some(SymbolFunc::Function(ref pc_name, _)),
            ..
        }) = pc.real_symbol()
        {
            if let Some(SymbolResult {
                image: ref lr_image,
                image_base: lr_base,
                function: Some(SymbolFunc::Function(ref lr_name, _)),
                ..
            }) = lr.real_symbol()
            {
                if pc_image == lr_image && pc_base == lr_base && pc_name == lr_name {
                    // remove LR, not PC. PC is the code we're actually running now;
                    // LR should not be in the stack
                    stack.remove(start_i + 1);
                }
            }
        }

        Ok(())
    }
}
