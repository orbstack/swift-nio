use std::{
    mem::{size_of, MaybeUninit},
    ops::Range,
};

#[allow(deprecated)] // mach2 doesn't have this
use mach2::{
    mach_types::thread_act_t,
    structs::arm_thread_state64_t,
    thread_act::{thread_get_state, thread_resume, thread_suspend},
    thread_status::ARM_THREAD_STATE64,
    vm_types::natural_t,
};
use vmm_ids::{ArcVcpuSignal, VcpuSignalMask};

use crate::check_mach;

use super::{
    time::MachAbsoluteTime,
    unwinder::{UnwindRegs, Unwinder, STACK_DEPTH_LIMIT},
};

#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, Ord, PartialOrd)]
pub struct ThreadId(pub thread_act_t);

#[derive(Debug)]
pub struct ProfileeThread {
    pub port: thread_act_t,
    pub vcpu_signal: Option<ArcVcpuSignal>,
    pub name: String,
}

impl ProfileeThread {
    pub fn id(&self) -> ThreadId {
        ThreadId(self.port)
    }

    fn get_unwind_regs(&self) -> anyhow::Result<UnwindRegs> {
        // get thread state
        let mut state = MaybeUninit::<arm_thread_state64_t>::uninit();
        let mut count = size_of::<arm_thread_state64_t>() as u32 / size_of::<natural_t>() as u32;
        unsafe {
            check_mach!(thread_get_state(
                self.port,
                ARM_THREAD_STATE64,
                state.as_mut_ptr() as *mut _,
                &mut count,
            ))?
        };
        let state = unsafe { state.assume_init() };

        Ok(UnwindRegs {
            pc: state.__pc,
            lr: state.__lr,
            fp: state.__fp,
            sp: state.__sp,
        })
    }

    pub fn sample(
        &self,
        host_unwinder: &mut impl Unwinder,
        hv_vcpu_run: &Option<Range<usize>>,
        hv_trap: &Option<Range<usize>>,
    ) -> anyhow::Result<(MachAbsoluteTime, Vec<u64>)> {
        // allocate stack upfront
        let mut stack = Vec::with_capacity(STACK_DEPTH_LIMIT);

        // suspend the thread
        unsafe { check_mach!(thread_suspend(self.port))? };

        // the most accurate timestamp is from when the thread has just been suspended (as that may take a while if it's in a kernel call), but before we spend time collecting the stack
        let timestamp = MachAbsoluteTime::now();

        let _guard = scopeguard::guard((), |_| unsafe {
            check_mach!(thread_resume(self.port)).unwrap();
        });

        /*
         ****** BEGIN CRITICAL SECTION ******
         * no allocations past this point;
         * could deadlock if suspended thread had malloc lock
         */

        // unwind the stack
        let regs = self.get_unwind_regs()?;
        host_unwinder.unwind(regs, |addr| stack.push(addr))?;

        // if thread is in HVF, trigger an exit now, so that it samples as soon as it resumes
        // for now we just check whether PC (stack[0]) is in hv_trap
        if let Some(hv_trap) = hv_trap {
            if let Some(&pc) = stack.first() {
                if hv_trap.contains(&(pc as usize)) {
                    if let Some(vcpu_signal) = &self.vcpu_signal {
                        vcpu_signal.assert(VcpuSignalMask::PROFILER_SAMPLE);
                    }
                }
            }
        }

        /*
         ****** END CRITICAL SECTION ******
         * scopeguard will resume the thread
         */
        drop(_guard);

        Ok((timestamp, stack))
    }
}
