use std::collections::VecDeque;

use crate::profiler::{symbolicator::SymbolResult, SymbolicatedFrame};

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
