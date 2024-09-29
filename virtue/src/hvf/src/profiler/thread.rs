use std::{
    collections::VecDeque,
    mem::{size_of, MaybeUninit},
    ops::Range,
};

use hdrhistogram::Histogram;
use libc::{
    proc_pidinfo, proc_threadinfo, thread_flavor_t, thread_identifier_info, thread_info,
    THREAD_IDENTIFIER_INFO, THREAD_IDENTIFIER_INFO_COUNT, TH_STATE_HALTED, TH_STATE_RUNNING,
    TH_STATE_STOPPED, TH_STATE_UNINTERRUPTIBLE, TH_STATE_WAITING,
};
use mach2::{mach_port::mach_port_deallocate, port::mach_port_t, traps::mach_task_self};
use mach2::{
    thread_act::{thread_get_state, thread_resume, thread_suspend},
    vm_types::natural_t,
};
use nix::errno::Errno;
use sysx::mach::time::MachAbsoluteTime;
use tracing::error;

use crate::{check_mach, ArcVcpuHandle};

use super::{
    symbolicator::HostKernelSymbolicator,
    transform::HostSyscallTransform,
    unwinder::{UnwindError, UnwindRegs, Unwinder, STACK_DEPTH_LIMIT},
    Frame, FrameCategory, MachError, MachResult, PartialSample, Sample, SampleStack,
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
    #[error("cpu time overflow")]
    CpuTimeOverflow,
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

#[derive(Debug, Copy, Clone, PartialEq, Eq)]
pub enum ThreadState {
    Running,
    Stopped,
    Waiting,
    Uninterruptible,
    Halted,
}

#[derive(Debug, Copy, Clone, Eq, PartialEq)]
struct ThreadCpuInfo {
    state: ThreadState,
    cpu_time_us: u64,

    raw_info: proc_threadinfo,
}

pub struct ProfileeThread {
    pub id: ThreadId,
    pub port: MachPort,
    pub vcpu: Option<ArcVcpuHandle>,
    pub name: Option<String>,
    pid: u32,

    last_cpu_info: Option<ThreadCpuInfo>,

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
            last_cpu_info: None,
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
        let info = unsafe { info.assume_init() };

        Ok(ThreadCpuInfo {
            // pth_flags|=TH_FLAGS_SWAPPED means preempted, but adding that makes stacks messy
            // TODO: add preemption markers instead
            state: match info.pth_run_state {
                TH_STATE_RUNNING => ThreadState::Running,
                TH_STATE_STOPPED => ThreadState::Stopped,
                TH_STATE_WAITING => ThreadState::Waiting,
                TH_STATE_UNINTERRUPTIBLE => ThreadState::Uninterruptible,
                TH_STATE_HALTED => ThreadState::Halted,
                _ => ThreadState::Running,
            },

            // kernel converts the time_value_t to nanoseconds (same as THREAD_EXTENDED_INFO)
            // but the native time_value_t unit is microseconds, so no point in storing more
            cpu_time_us: info.pth_user_time / 1000 + info.pth_system_time / 1000,

            raw_info: info,
        })
    }

    #[cfg(target_arch = "aarch64")]
    fn get_unwind_regs(&self) -> MachResult<UnwindRegs> {
        use mach2::{structs::arm_thread_state64_t, thread_status::ARM_THREAD_STATE64};

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
            #[cfg(feature = "profiler-framehop")]
            sp: state.__sp,

            syscall_num: state.__x[16],
        })
    }

    #[cfg(target_arch = "x86_64")]
    fn get_unwind_regs(&self) -> MachResult<UnwindRegs> {
        use mach2::{structs::x86_thread_state64_t, thread_status::x86_THREAD_STATE64};

        // get thread state
        let mut state = MaybeUninit::<x86_thread_state64_t>::uninit();
        let mut count = size_of::<x86_thread_state64_t>() as u32 / size_of::<natural_t>() as u32;
        unsafe {
            check_mach!(thread_get_state(
                self.port.0,
                x86_THREAD_STATE64,
                state.as_mut_ptr() as *mut _,
                &mut count,
            ))?
        };
        let state = unsafe { state.assume_init() };

        Ok(UnwindRegs {
            pc: state.__rip,
            lr: state.__rbp,
            fp: state.__rbp,
            #[cfg(feature = "profiler-framehop")]
            sp: state.__rsp,

            syscall_num: state.__rax,
        })
    }

    // can't use anyhow::Result here: it allocates on error,
    // and we need to make sure this never allocates while another thread is suspended
    pub fn sample(
        &mut self,
        host_unwinder: &mut impl Unwinder,
        thread_suspend_histogram: &mut Histogram<u64>,
        hv_vcpu_run: Range<usize>,
    ) -> Result<SampleResult, SampleError> {
        let mut info = match self.get_cpu_info() {
            Ok(info) => info,
            // thread is gone
            Err(Errno::ESRCH) => {
                self.stopped_at = Some(MachAbsoluteTime::now());
                return Ok(SampleResult::ThreadStopped);
            }
            Err(e) => return Err(SampleError::ThreadInfo(e)),
        };

        if info.state != ThreadState::Running && self.last_cpu_info == Some(info) {
            // no CPU time elapsed, thread state hasn't changed, thread isn't running, not first sample
            let now = MachAbsoluteTime::now();
            return Ok(SampleResult::Sample(Sample {
                sample_begin_time: now,
                timestamp_offset: 0,
                cpu_time_delta_us: 0,
                thread_id: self.id,
                thread_state: info.state,
                stack: SampleStack::SameAsLast,
            }));
        }

        let Some(cpu_time_delta_us) = info
            .cpu_time_us
            .checked_sub(self.last_cpu_info.map_or(0, |i| i.cpu_time_us))
        else {
            // subtraction overflowed, meaning that cpu time went backwards
            // macOS kernel bug: this can happen if we race and read info from a stopping thread
            if info.cpu_time_us == 0 {
                return Ok(SampleResult::ThreadStopped);
            } else {
                // should never happen
                return Err(SampleError::CpuTimeOverflow);
            }
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

        // get registers
        let regs = self
            .get_unwind_regs()
            .map_err(SampleError::ThreadGetState)?;

        // save cpu info before changing it
        self.last_cpu_info = Some(info);

        // add a synthetic kernel frame if we're in a syscall
        let in_syscall = HostSyscallTransform::is_syscall_pc(regs.pc);
        if in_syscall {
            // XNU ABI: syscall number is in x16
            let syscall_num = regs.syscall_num;
            stack.push_back(Frame::new(
                FrameCategory::HostKernel,
                HostKernelSymbolicator::addr_for_syscall(syscall_num as i64),
            ));
        } else if info.state == ThreadState::Waiting || info.state == ThreadState::Uninterruptible {
            // if we're not in a syscall, it's not possible for us to be in thread_wait or uninterruptible
            // this was a race: thread was waiting when we checked the state, but it returned before we suspended it
            // must change this *after* saving, so that last_cpu_info check works when transitioning from Waiting -> Running
            info.state = ThreadState::Running;
        }

        // unwind the stack
        host_unwinder
            .unwind(regs, |addr| {
                stack.push_back(Frame::new(FrameCategory::HostUserspace, addr))
            })
            .map_err(SampleError::Unwind)?;

        let sample = Sample {
            sample_begin_time: before_suspend,
            timestamp_offset: (timestamp - before_suspend).0 as u32,
            cpu_time_delta_us: cpu_time_delta_us as u32,
            thread_id: self.id,
            thread_state: info.state,
            stack: SampleStack::Stack(stack),
        };

        // if thread is in HVF, trigger an exit now, so that it samples as soon as it resumes
        // we check for whether it's in hv_vcpu_run's HV syscall
        if in_syscall
            && regs.syscall_num == HostSyscallTransform::SYSCALL_MACH_HV_TRAP_ARM64
            // LR=hv_vcpu_run because PC=hv_trap
            && hv_vcpu_run.contains(&(regs.lr as usize))
        {
            if let Some(vcpu) = &self.vcpu {
                let _ = vcpu.profiler_sample.send(PartialSample { sample });
                // resumes thread
                return Ok(SampleResult::Queued);
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
