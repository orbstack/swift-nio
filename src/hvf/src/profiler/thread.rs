use std::{
    collections::VecDeque,
    mem::{size_of, MaybeUninit},
    ops::Range,
    sync::Arc,
};

use libc::{
    thread_basic_info, thread_flavor_t, thread_info, time_value_t, THREAD_BASIC_INFO,
    THREAD_BASIC_INFO_COUNT,
};
#[allow(deprecated)] // mach2 doesn't have this
use mach2::{
    mach_types::thread_act_t,
    structs::arm_thread_state64_t,
    thread_act::{thread_get_state, thread_resume, thread_suspend},
    thread_status::ARM_THREAD_STATE64,
    vm_types::natural_t,
};

use crate::{check_mach, ArcVcpuHandle};

use super::{
    symbolicator::Symbolicator,
    time::MachAbsoluteTime,
    unwinder::{UnwindRegs, Unwinder, STACK_DEPTH_LIMIT},
    Frame, PartialSample, Profiler, Sample, SampleCategory,
};

pub enum SampleResult {
    Sample(Sample),
    Queued,
}

#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, Ord, PartialOrd)]
pub struct ThreadId(pub thread_act_t);

trait TimeValueExt {
    fn as_micros(&self) -> u64;
}

impl TimeValueExt for time_value_t {
    fn as_micros(&self) -> u64 {
        (self.seconds as u64) * 1_000_000 + (self.microseconds as u64)
    }
}

pub struct ProfileeThread {
    pub port: thread_act_t,
    pub vcpu: Option<ArcVcpuHandle>,
    pub name: String,

    pub last_cpu_time_us: Option<u64>,

    pub added_at: MachAbsoluteTime,
    pub stopped_at: Option<MachAbsoluteTime>,
}

impl ProfileeThread {
    pub fn id(&self) -> ThreadId {
        ThreadId(self.port)
    }

    fn get_info(&self) -> anyhow::Result<thread_basic_info> {
        let mut info = MaybeUninit::<thread_basic_info>::uninit();
        let mut info_count = THREAD_BASIC_INFO_COUNT;
        unsafe {
            check_mach!(thread_info(
                self.port,
                THREAD_BASIC_INFO as thread_flavor_t,
                &mut info as *mut _ as *mut _,
                &mut info_count,
            ))?
        };

        let info = unsafe { info.assume_init() };
        Ok(info)
    }

    pub fn cpu_time_delta_us(&mut self) -> anyhow::Result<u64> {
        let info = self.get_info()?;
        let cpu_time_us = info.user_time.as_micros() + info.system_time.as_micros();

        let delta = cpu_time_us - self.last_cpu_time_us.unwrap_or(cpu_time_us);
        self.last_cpu_time_us = Some(cpu_time_us);
        Ok(delta)
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
        profiler: &Arc<Profiler>,
        host_unwinder: &mut impl Unwinder,
        framehop_unwinder: &mut impl Unwinder,
        symbolicator: &impl Symbolicator,
        hv_vcpu_run: &Option<Range<usize>>,
        hv_trap: &Option<Range<usize>>,
    ) -> anyhow::Result<SampleResult> {
        let sample = Sample {
            timestamp: MachAbsoluteTime::dummy(),
            cpu_time_delta_us: 0,

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
        framehop_unwinder.unwind(regs, |addr| {
            sample
                .stack
                .push_back(Frame::new(SampleCategory::HostUserspace, addr))
        })?;

        // if thread is in HVF, trigger an exit now, so that it samples as soon as it resumes
        // for now we just check whether PC (stack[0]) is in hv_trap
        if let Some(hv_vcpu_run) = hv_vcpu_run {
            if let Some(&frame) = sample.stack.get(1) {
                if hv_vcpu_run.contains(&(frame.addr as usize)) {
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
