use std::{
    collections::VecDeque,
    mem::{size_of, MaybeUninit},
    ops::Range,
};

use hdrhistogram::Histogram;
use libc::{
    thread_basic_info, thread_flavor_t, thread_info, time_value_t, THREAD_BASIC_INFO,
    THREAD_BASIC_INFO_COUNT,
};
use mach2::{mach_port::mach_port_deallocate, traps::mach_task_self};
#[allow(deprecated)] // mach2 doesn't have this
use mach2::{
    mach_types::thread_act_t,
    structs::arm_thread_state64_t,
    thread_act::{thread_get_state, thread_resume, thread_suspend},
    thread_status::ARM_THREAD_STATE64,
    vm_types::natural_t,
};
use tracing::error;

use crate::{check_mach, ArcVcpuHandle};

use super::{
    time::MachAbsoluteTime,
    unwinder::{UnwindError, UnwindRegs, Unwinder, STACK_DEPTH_LIMIT},
    Frame, MachError, MachResult, PartialSample, Sample, SampleCategory, SampleStack,
};

pub enum SampleResult {
    Sample(Sample),
    Queued,
    ThreadStopped,
}

#[derive(thiserror::Error, Debug)]
pub enum SampleError {
    #[error("thread_get_info: {0}")]
    ThreadGetInfo(MachError),
    #[error("thread_suspend: {0}")]
    ThreadSuspend(MachError),
    #[error("thread_get_state: {0}")]
    ThreadGetState(MachError),
    #[error("unwind: {0}")]
    Unwind(UnwindError),
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
    pub fn new(port: thread_act_t, name: String, vcpu: Option<ArcVcpuHandle>) -> Self {
        Self {
            port,
            vcpu,
            name,
            last_cpu_time_us: None,
            added_at: MachAbsoluteTime::now(),
            stopped_at: None,
        }
    }

    pub fn id(&self) -> ThreadId {
        ThreadId(self.port)
    }

    fn get_info(&self) -> MachResult<thread_basic_info> {
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

    fn get_cpu_time_delta_us(&mut self) -> MachResult<Option<u64>> {
        let info = self.get_info()?;
        let cpu_time_us = info.user_time.as_micros() + info.system_time.as_micros();

        let delta = self.last_cpu_time_us.map(|last| cpu_time_us - last);
        self.last_cpu_time_us = Some(cpu_time_us);
        Ok(delta)
    }

    fn get_unwind_regs(&self) -> MachResult<UnwindRegs> {
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

    // can't use anyhow::Result here: it allocates on error,
    // and we need to make sure this never allocates while another thread is suspended
    pub fn sample(
        &mut self,
        host_unwinder: &mut impl Unwinder,
        thread_suspend_histogram: &mut Histogram<u64>,
        hv_vcpu_run: &Option<Range<usize>>,
        hv_trap: &Option<Range<usize>>,
    ) -> Result<SampleResult, SampleError> {
        let cpu_time_delta_us = match self.get_cpu_time_delta_us() {
            // no CPU time elapsed; thread is idle, and this isn't the first sample
            Ok(Some(0)) => {
                return Ok(SampleResult::Sample(Sample {
                    timestamp: MachAbsoluteTime::now(),
                    cpu_time_delta_us: 0,
                    thread_id: self.id(),
                    stack: SampleStack::SameAsLast,
                }))
            }

            // some CPU has been used
            Ok(delta) => delta,

            // thread is gone
            Err(MachError::MachSendInvalidDest) => {
                self.stopped_at = Some(MachAbsoluteTime::now());
                return Ok(SampleResult::ThreadStopped);
            }

            Err(e) => return Err(SampleError::ThreadGetInfo(e)),
        };

        // TODO: enforce limit including guest frames
        // allocate stack upfront
        // MUST not allocate on .push
        let mut stack = VecDeque::with_capacity(STACK_DEPTH_LIMIT);

        // suspend the thread
        unsafe { check_mach!(thread_suspend(self.port)).map_err(SampleError::ThreadSuspend)? };
        let suspend_begin = MachAbsoluteTime::now();

        /*
         ****** BEGIN CRITICAL SECTION ******
         * no allocations past this point;
         * could deadlock if suspended thread had malloc lock
         */

        let _guard = scopeguard::guard((), |_| {
            match unsafe { check_mach!(thread_resume(self.port)) } {
                Ok(_) => {}
                Err(MachError::MachSendInvalidDest) => {
                    // thread was dying
                    return;
                }
                Err(e) => {
                    error!("failed to resume thread: {:?}", e);
                }
            }

            thread_suspend_histogram
                .record((MachAbsoluteTime::now() - suspend_begin).nanos())
                .unwrap();
        });

        // the most accurate timestamp is from when the thread has just been suspended (as that may take a while if it's in a kernel call), but before we spend time collecting the stack
        let timestamp = MachAbsoluteTime::now();

        // unwind the stack
        let regs = self
            .get_unwind_regs()
            .map_err(SampleError::ThreadGetState)?;
        host_unwinder
            .unwind(regs, |addr| {
                stack.push_back(Frame::new(SampleCategory::HostUserspace, addr))
            })
            .map_err(SampleError::Unwind)?;

        let sample = Sample {
            timestamp,
            cpu_time_delta_us: cpu_time_delta_us.unwrap_or(0) as u32,
            thread_id: self.id(),
            stack: SampleStack::Stack(stack),
        };

        // if thread is in HVF, trigger an exit now, so that it samples as soon as it resumes
        // for now we just check whether PC (stack[0]) is in hv_trap
        if let Some(hv_vcpu_run) = hv_vcpu_run {
            let SampleStack::Stack(stack) = &sample.stack else {
                panic!("stack is not a stack");
            };
            if let Some(&frame) = stack.get(1) {
                if hv_vcpu_run.contains(&(frame.addr as usize)) {
                    if let Some(vcpu) = &self.vcpu {
                        vcpu.send_profiler_sample(PartialSample { sample });
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

        Ok(SampleResult::Sample(sample))
    }
}

impl Drop for ProfileeThread {
    fn drop(&mut self) {
        unsafe { check_mach!(mach_port_deallocate(mach_task_self(), self.port)).unwrap() };
    }
}
