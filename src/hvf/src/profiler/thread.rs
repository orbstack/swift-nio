use std::{
    collections::VecDeque,
    mem::{size_of, MaybeUninit},
    ops::Range,
};

use hdrhistogram::Histogram;
use libc::{
    proc_pidinfo, proc_threadinfo, thread_flavor_t, thread_identifier_info, thread_info,
    THREAD_IDENTIFIER_INFO, THREAD_IDENTIFIER_INFO_COUNT,
};
use mach2::{mach_port::mach_port_deallocate, port::mach_port_t, traps::mach_task_self};
#[allow(deprecated)] // mach2 doesn't have this
use mach2::{
    structs::arm_thread_state64_t,
    thread_act::{thread_get_state, thread_resume, thread_suspend},
    thread_status::ARM_THREAD_STATE64,
    vm_types::natural_t,
};
use nix::errno::Errno;
use tracing::error;

use crate::{check_mach, ArcVcpuHandle};

use super::{
    time::MachAbsoluteTime,
    transform::SyscallTransform,
    unwinder::{UnwindError, UnwindRegs, Unwinder, STACK_DEPTH_LIMIT},
    Frame, MachError, MachResult, PartialSample, Sample, SampleCategory, SampleStack,
};

const PROC_PIDTHREADID64INFO: i32 = 15;

pub enum SampleResult {
    Sample(Sample),
    Queued,
    ThreadStopped,
}

#[derive(thiserror::Error, Debug)]
pub enum SampleError {
    #[error("thread_info: {0}")]
    ThreadInfo(nix::Error),
    #[error("thread_suspend: {0}")]
    ThreadSuspend(MachError),
    #[error("thread_get_state: {0}")]
    ThreadGetState(MachError),
    #[error("unwind: {0}")]
    Unwind(UnwindError),
}

#[derive(Debug)]
pub struct MachPort(pub mach_port_t);

impl MachPort {
    pub unsafe fn from_raw(port: mach_port_t) -> Self {
        Self(port)
    }
}

impl Drop for MachPort {
    fn drop(&mut self) {
        unsafe { check_mach!(mach_port_deallocate(mach_task_self(), self.0)).unwrap() };
    }
}

#[derive(Debug, Copy, Clone, PartialEq, Eq, Hash, Ord, PartialOrd)]
pub struct ThreadId(pub u64);

impl ThreadId {
    pub(crate) fn from_port(port: &MachPort) -> MachResult<Self> {
        let mut info = MaybeUninit::<thread_identifier_info>::uninit();
        let mut info_count = THREAD_IDENTIFIER_INFO_COUNT;
        unsafe {
            check_mach!(thread_info(
                port.0,
                THREAD_IDENTIFIER_INFO as thread_flavor_t,
                &mut info as *mut _ as *mut _,
                &mut info_count,
            ))?
        };

        let info = unsafe { info.assume_init() };
        Ok(Self(info.thread_id))
    }
}

#[derive(Debug, Copy, Clone)]
struct ThreadCpuInfo {
    user_time_us: u64,
    system_time_us: u64,
}

pub struct ProfileeThread {
    pub id: ThreadId,
    pub port: MachPort,
    pub vcpu: Option<ArcVcpuHandle>,
    pub name: Option<String>,
    pid: u32,

    pub last_cpu_time_us: Option<u64>,

    pub added_at: MachAbsoluteTime,
    pub stopped_at: Option<MachAbsoluteTime>,
}

impl ProfileeThread {
    pub fn new(
        id: ThreadId,
        port: MachPort,
        name: Option<String>,
        vcpu: Option<ArcVcpuHandle>,
    ) -> Self {
        Self {
            id,
            port,
            vcpu,
            name,
            pid: std::process::id(),
            last_cpu_time_us: None,
            added_at: MachAbsoluteTime::now(),
            stopped_at: None,
        }
    }

    pub fn display_name(&self) -> String {
        self.name
            .clone()
            .unwrap_or_else(|| format!("{:#x}", self.id.0))
    }

    fn get_cpu_info(&self) -> nix::Result<ThreadCpuInfo> {
        let mut info = MaybeUninit::<proc_threadinfo>::uninit();
        let ret = unsafe {
            proc_pidinfo(
                self.pid as i32,
                PROC_PIDTHREADID64INFO,
                self.id.0,
                info.as_mut_ptr() as *mut _,
                size_of::<proc_threadinfo>() as i32,
            )
        };
        Errno::result(ret)?;

        // pth_flags and pth_run_state:
        // run_state = TH_STATE_RUNNING, flags = 0: running
        // run_state = TH_STATE_RUNNING, flags = TH_FLAGS_SWAPPED: preempted
        // run_state = TH_STATE_WAITING: interruptible wait
        //   * why is there both flags=SWAPPED and flags=0 for this?
        // run_state = TH_STATE_UNINTERRUPTIBLE: uninterruptible wait

        let info = unsafe { info.assume_init() };
        Ok(ThreadCpuInfo {
            // kernel converts the time_value_t to nanoseconds (same as THREAD_EXTENDED_INFO)
            // but the native time_value_t unit is microseconds, so no point in storing more
            user_time_us: info.pth_user_time / 1000,
            system_time_us: info.pth_system_time / 1000,
        })
    }

    fn get_cpu_time_delta_us(&mut self) -> nix::Result<Option<u64>> {
        let info = self.get_cpu_info()?;
        let cpu_time_us = info.user_time_us + info.system_time_us;
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
                self.port.0,
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
    ) -> Result<SampleResult, SampleError> {
        let cpu_time_delta_us = match self.get_cpu_time_delta_us() {
            // no CPU time elapsed; thread is idle, and this isn't the first sample
            Ok(Some(0)) => {
                let now = MachAbsoluteTime::now();
                return Ok(SampleResult::Sample(Sample {
                    time: now,
                    sample_begin_time: now,
                    cpu_time_delta_us: 0,
                    thread_id: self.id,
                    stack: SampleStack::SameAsLast,
                }));
            }

            // some CPU has been used; not first sample
            Ok(Some(delta)) => delta as u32,

            // first sample
            Ok(None) => 0,

            // thread is gone
            Err(Errno::ESRCH) => {
                self.stopped_at = Some(MachAbsoluteTime::now());
                return Ok(SampleResult::ThreadStopped);
            }

            Err(e) => return Err(SampleError::ThreadInfo(e)),
        };

        // TODO: enforce limit including guest frames
        // allocate stack upfront
        // MUST not allocate on .push
        let mut stack = VecDeque::with_capacity(STACK_DEPTH_LIMIT);

        // suspend the thread
        let before_suspend = MachAbsoluteTime::now();
        unsafe { check_mach!(thread_suspend(self.port.0)).map_err(SampleError::ThreadSuspend)? };
        let suspend_begin = MachAbsoluteTime::now();

        /*
         ****** BEGIN CRITICAL SECTION ******
         * no allocations past this point;
         * could deadlock if suspended thread had malloc lock
         */

        let _guard = scopeguard::guard((), |_| {
            match unsafe { check_mach!(thread_resume(self.port.0)) } {
                Ok(_) => {}
                Err(
                    MachError::InvalidArgument
                    | MachError::Terminated
                    | MachError::MachSendInvalidDest,
                ) => {
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
            time: timestamp,
            sample_begin_time: before_suspend,
            cpu_time_delta_us,
            thread_id: self.id,
            stack: SampleStack::Stack(stack),
        };

        // if thread is in HVF, trigger an exit now, so that it samples as soon as it resumes
        // we check for whether it's in hv_vcpu_run, and specifically in the HV syscall
        // checking for PC in hv_trap is easier, but that's a private symbol so dlsym() can't find it,
        // and mmapping from dyld shared cache (to get __LINKEDIT) is complicated
        // without this, we sample kernel stacks for internal calls like _hv_vcpu_set_control_field when the guest wasn't running
        if let Some(hv_vcpu_run) = hv_vcpu_run {
            let SampleStack::Stack(stack) = &sample.stack else {
                panic!("stack is not a stack");
            };

            if let Some(&frame) = stack.get(1) {
                if hv_vcpu_run.contains(&(frame.addr as usize))
                    && SyscallTransform::is_syscall_pc(stack[0].addr)
                {
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
