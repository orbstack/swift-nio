use std::collections::VecDeque;

use crate::profiler::{
    memory::read_host_mem_aligned,
    symbolicator::{HostKernelSymbolicator, SymbolResult},
    Frame, SampleCategory, SymbolicatedFrame,
};

use super::StackTransform;

const ARM64_INSN_SIZE: u64 = 4;
const ARM64_INSN_SVC_0X80: u32 = 0xd4001001;

pub struct HostSyscallTransform {}

impl HostSyscallTransform {
    pub fn is_syscall_pc(pc: u64) -> bool {
        // in a syscall, PC = return address from syscall, incremented by the CPU when it takes the exception
        // so PC - 4 = syscall instruction
        // if that's the PC from thread sampling, then we are almost certainly in a syscall
        // (the instruction immediately after a syscall shouldn't be slow)
        let svc_pc = pc - ARM64_INSN_SIZE;

        // XNU uses "svc 0x80" which assembles to 0xd4001001
        // read is safe: instructions should always be aligned
        if let Some(insn) = unsafe { read_host_mem_aligned::<u32>(svc_pc) } {
            insn == ARM64_INSN_SVC_0X80
        } else {
            false
        }
    }
}

impl StackTransform for HostSyscallTransform {
    fn transform(&self, stack: &mut VecDeque<SymbolicatedFrame>) -> anyhow::Result<()> {
        // find the first host userspace frame, not necessarily the front of the stack
        // this makes it work for hv_trap, nested MACH_vmfaults in syscalls, etc.
        let Some((index, pc)) = stack
            .iter()
            .enumerate()
            .find(|&(_, sframe)| sframe.frame.category == SampleCategory::HostUserspace)
        else {
            return Ok(());
        };

        if HostSyscallTransform::is_syscall_pc(pc.frame.addr) {
            // derive a syscall name from the userspace caller's symbol
            // this isn't really accurate, but it almost always works because macOS requires libSystem
            // we could do better by reading and saving x16, but that adds the overhead of reading the instruction at PC (and risking faults) to the thread-suspended critical section
            // with x16: read x16 as i64. if negative, trace_code = (Mach) 0x10c0000 + (-x16) * 4. if positive, trace code = (BSD) 0x40c0000 + (x16) * 4. look up code in /usr/share/misc/trace.codes
            let syscall_name = match pc.symbol {
                Some(SymbolResult {
                    symbol_offset: Some((ref name, _)),
                    ..
                }) => name,
                _ => "<unknown>",
            };

            // insert a syscall frame below this
            stack.insert(
                index,
                SymbolicatedFrame {
                    frame: Frame {
                        category: SampleCategory::HostKernel,
                        addr: pc.frame.addr,
                    },
                    symbol: Some(SymbolResult {
                        image: HostKernelSymbolicator::IMAGE.to_string(),
                        image_base: 0,
                        symbol_offset: Some((format!("syscall: {}", syscall_name), 0)),
                    }),
                },
            );
        }

        Ok(())
    }
}
