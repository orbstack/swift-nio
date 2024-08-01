use std::{
    collections::VecDeque,
    mem::{size_of, MaybeUninit},
    ops::Range,
    sync::Arc,
};

#[allow(deprecated)] // mach2 doesn't have this
use mach2::{
    mach_types::thread_act_t,
    structs::arm_thread_state64_t,
    thread_act::{thread_get_state, thread_resume, thread_suspend},
    thread_status::ARM_THREAD_STATE64,
    vm_types::natural_t,
};
use vmm_ids::VcpuSignalMask;

use crate::{check_mach, ArcVcpuHandle};

use super::{
    symbolicator::Symbolicator,
    time::MachAbsoluteTime,
    unwinder::{UnwindRegs, Unwinder, STACK_DEPTH_LIMIT},
    PartialSample, Profiler, Sample, SampleCategory,
};

pub enum SampleResult {
    Sample(Sample),
    Queued,
}

#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, Ord, PartialOrd)]
pub struct ThreadId(pub thread_act_t);

pub struct ProfileeThread {
    pub port: thread_act_t,
    pub vcpu: Option<ArcVcpuHandle>,
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
            x28: state.__x[28],
        })
    }

    pub fn sample(
        &self,
        profiler: &Arc<Profiler>,
        host_unwinder: &mut impl Unwinder,
        framehop_unwinder: &mut impl Unwinder,
        symbolicator: &impl Symbolicator,
        hv_vcpu_run: &Option<Range<usize>>,
        hv_trap: &Option<Range<usize>>,
    ) -> anyhow::Result<SampleResult> {
        let sample = Sample {
            timestamp: MachAbsoluteTime::dummy(),
            cpu_time: 0,

            thread_id: self.id(),

            // TODO: enforce limit including guest frames
            // allocate stack upfront
            // MUST not allocate on .push
            stack: VecDeque::with_capacity(STACK_DEPTH_LIMIT),
        };

        let mut partial_sample = Box::new(PartialSample {
            inner: sample,
            profiler: profiler.clone(),
        });
        let sample = &mut partial_sample.inner;

        // suspend the thread
        unsafe { check_mach!(thread_suspend(self.port))? };

        /*
         ****** BEGIN CRITICAL SECTION ******
         * no allocations past this point;
         * could deadlock if suspended thread had malloc lock
         */

        let _guard = scopeguard::guard((), |_| unsafe {
            check_mach!(thread_resume(self.port)).unwrap();
        });

        // the most accurate timestamp is from when the thread has just been suspended (as that may take a while if it's in a kernel call), but before we spend time collecting the stack
        sample.timestamp = MachAbsoluteTime::now();

        // unwind the stack
        let regs = self.get_unwind_regs()?;
        host_unwinder.unwind(regs, |addr| {
            sample
                .stack
                .push_back((SampleCategory::HostUserspace, addr))
        })?;

        // if thread is in HVF, trigger an exit now, so that it samples as soon as it resumes
        // for now we just check whether PC (stack[0]) is in hv_trap
        if let Some(hv_vcpu_run) = hv_vcpu_run {
            if let Some(&(_, pc)) = sample.stack.get(1) {
                if hv_vcpu_run.contains(&(pc as usize)) {
                    if let Some(vcpu) = &self.vcpu {
                        vcpu.send_profiler_sample(partial_sample);
                        // resumes thread
                        return Ok(SampleResult::Queued);
                    }
                }
            }
        }

        /*
         ****** END CRITICAL SECTION ******
         * scopeguard will resume the thread
         */
        drop(_guard);

        Ok(SampleResult::Sample(sample.clone()))
    }
}
