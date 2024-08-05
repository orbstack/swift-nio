use std::{
    collections::VecDeque,
    ffi::CStr,
    mem::size_of,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    thread::JoinHandle,
    time::{Duration, SystemTime},
};

use ahash::AHashMap;
use anyhow::anyhow;
use buffer::SegVec;
use crossbeam::queue::ArrayQueue;
use crossbeam_channel::Sender;
use exporter::{FirefoxExporter, TextExporter};
use hdrhistogram::Histogram;
use ktrace::{KtraceResults, Ktracer};
use libc::{
    thread_extended_info, thread_flavor_t, thread_info, THREAD_EXTENDED_INFO,
    THREAD_EXTENDED_INFO_COUNT,
};
use mach2::{
    kern_return::{kern_return_t, KERN_INVALID_ARGUMENT, KERN_TERMINATED},
    mach_time::mach_wait_until,
    mach_types::{thread_act_array_t, thread_act_t},
    message::{mach_msg_type_number_t, MACH_SEND_INVALID_DEST},
    task::task_threads,
    traps::mach_task_self,
    vm::mach_vm_deallocate,
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use sched::set_realtime_scheduling;
use serde::{Deserialize, Serialize};
use server::FirefoxApiServer;
use symbolicator::{
    CachedSymbolicator, DladdrSymbolicator, HostKernelSymbolicator, LinuxSymbolicator,
    SymbolResult, Symbolicator,
};
use thread::{MachPort, ProfileeThread, SampleError, SampleResult, ThreadId};
use time::{MachAbsoluteDuration, MachAbsoluteTime};
use tracing::{error, info, warn};
use transform::{
    CgoTransform, HostSyscallTransform, LeafCallTransform, LinuxIrqTransform, StackTransform,
};
use unwinder::FramePointerUnwinder;
use utils::{
    qos::{self, QosClass},
    Mutex,
};

use crate::{VcpuHandleInner, VcpuRegistry};

mod buffer;
mod exporter;
mod ktrace;
mod memory;
mod sched;
mod server;
pub mod stats;
pub mod symbolicator;
mod thread;
pub(crate) mod time;
mod transform;
mod unwinder;

pub use unwinder::STACK_DEPTH_LIMIT;

// 50 threads * 1000 Hz * 5 seconds
const SEGMENT_SIZE: usize = 50 * 1000 * 5;

const MIN_SAMPLE_INTERVAL: Duration = Duration::from_micros(100);
const MAX_SAMPLE_INTERVAL: Duration = Duration::from_secs(2);

pub(crate) const THREAD_NAME_TAG: &str = "PROFILER";

// use a macro to preserve anyhow stack trace
#[macro_export]
macro_rules! check_mach {
    ($ret:expr) => {
        if $ret == mach2::kern_return::KERN_SUCCESS {
            Ok(())
        } else {
            Err($crate::profiler::MachError::from_ret($ret))
        }
    };
}

#[derive(thiserror::Error, Debug)]
pub enum MachError {
    // kernel RPC server normally deallocates ports so that dead threads return MACH_SEND_INVALID_DEST, but sometimes there's a race: INVALID_ARGUMENT = couldn't find thread, and TERMINATED = thread still exists but is in terminated state
    #[error("INVALID_ARGUMENT")]
    InvalidArgument,
    #[error("TERMINATED")]
    Terminated,

    #[error("MACH_SEND_INVALID_DEST")]
    MachSendInvalidDest,
    #[error("mach error: {0}")]
    Other(kern_return_t),
}

impl MachError {
    fn from_ret(ret: kern_return_t) -> Self {
        match ret {
            KERN_INVALID_ARGUMENT => Self::InvalidArgument,
            KERN_TERMINATED => Self::Terminated,
            MACH_SEND_INVALID_DEST => Self::MachSendInvalidDest,
            _ => Self::Other(ret),
        }
    }
}

pub type MachResult<T> = Result<T, MachError>;

#[derive(Debug, Copy, Clone, Ord, PartialOrd, Eq, PartialEq, Hash)]
pub enum SampleCategory {
    GuestUserspace,
    GuestKernel,
    HostUserspace,
    HostKernel,
}

impl SampleCategory {
    fn as_char(&self) -> char {
        match self {
            SampleCategory::GuestUserspace => 'G',
            SampleCategory::GuestKernel => 'K',
            SampleCategory::HostUserspace => 'U',
            SampleCategory::HostKernel => 'H',
        }
    }

    pub fn is_guest(&self) -> bool {
        matches!(
            self,
            SampleCategory::GuestUserspace | SampleCategory::GuestKernel
        )
    }

    pub fn is_host(&self) -> bool {
        matches!(
            self,
            SampleCategory::HostUserspace | SampleCategory::HostKernel
        )
    }
}

#[derive(Debug)]
struct Sample {
    // when the sample was actually collected (i.e. after thread_suspend)
    time: MachAbsoluteTime,
    // when we started trying to collect the sample (i.e. before thread_suspend)
    sample_begin_time: MachAbsoluteTime,
    cpu_time_delta_us: u32,
    thread_id: ThreadId,
    stack: SampleStack,
}

#[derive(Debug)]
enum SampleStack {
    Stack(VecDeque<Frame>),
    // doesn't change size because VecDeque uses Unique for its pointer
    SameAsLast,
}

#[derive(Debug, Copy, Clone, PartialOrd, PartialEq, Eq, Ord, Hash)]
pub struct Frame {
    category: SampleCategory,
    addr: u64,
}

impl Frame {
    pub fn new(category: SampleCategory, addr: u64) -> Self {
        Self { category, addr }
    }
}

#[derive(Debug)]
pub(crate) struct SymbolicatedFrame {
    frame: Frame,
    symbol: Option<SymbolResult>,
}

pub struct PartialSample {
    sample: Sample,
}

impl PartialSample {
    pub fn timestamp(&self) -> MachAbsoluteTime {
        self.sample.time
    }

    pub fn prepend_stack(&mut self, frame: Frame) {
        if let SampleStack::Stack(stack) = &mut self.sample.stack {
            stack.push_front(frame);
        } else {
            panic!("cannot prepend to non-stack sample");
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProfilerParams {
    sample_rate: u64,
    duration_ms: Option<u64>,
    output_path: String,

    app_build_number: Option<u32>,
    app_version: Option<String>,
    app_commit: Option<String>,
}

pub struct ProfilerGuestContext {
    pub symbolicator: Option<LinuxSymbolicator>,
}

pub struct VcpuProfilerResults {
    pub histograms: VcpuHistograms,
}

pub struct VcpuHistograms {
    pub sample_time: Histogram<u64>,
    pub resume_and_sample: Histogram<u64>,
}

impl VcpuHistograms {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            sample_time: Histogram::<u64>::new(3)?,
            resume_and_sample: Histogram::<u64>::new(3)?,
        })
    }
}

pub struct ProfilerVcpuInit {
    pub profiler: Arc<Profiler>,
    pub completion_sender: Sender<()>,
}

#[derive(Debug, Clone)]
pub struct ProfileInfo {
    pub pid: u32,
    pub start_time: SystemTime,
    pub start_time_abs: MachAbsoluteTime,
    pub end_time: SystemTime,
    pub end_time_abs: MachAbsoluteTime,
    pub params: ProfilerParams,
    pub num_samples: usize,
}

pub struct ProfileResults {
    info: ProfileInfo,
    samples: SegVec<Sample, SEGMENT_SIZE>,
    threads: Vec<ProfileeThread>,
    resources: SegVec<ResourceSample, SEGMENT_SIZE>,
    sample_batch_histogram: Histogram<u64>,
    thread_suspend_histogram: Histogram<u64>,
    vcpu_agg_histograms: VcpuHistograms,
    ktrace: Option<KtraceResults>,
}

struct ThreadFrameState<'a> {
    sframes_buf: VecDeque<SymbolicatedFrame>,
    last_orig_stack: Option<&'a VecDeque<Frame>>,
}

impl ThreadFrameState<'_> {
    fn new() -> Self {
        Self {
            sframes_buf: VecDeque::new(),
            last_orig_stack: None,
        }
    }
}

pub struct ResourceSample {
    pub time: MachAbsoluteTime,
    pub phys_footprint: i64,
}

pub struct Profiler {
    vcpu_registry: Arc<dyn VcpuRegistry>,
    params: ProfilerParams,

    stop: AtomicBool,
    join_handles: Mutex<Option<Vec<JoinHandle<()>>>>,

    ingest_queue: ArrayQueue<Sample>,
}

impl Profiler {
    pub fn new(params: ProfilerParams, vcpu_registry: Arc<dyn VcpuRegistry>) -> Self {
        let num_vcpus = vcpu_registry.num_vcpus();
        Self {
            vcpu_registry,
            params,
            stop: AtomicBool::new(false),
            join_handles: Mutex::new(None),
            ingest_queue: ArrayQueue::new(num_vcpus),
        }
    }

    pub fn start(self: &Arc<Self>) -> anyhow::Result<()> {
        info!("starting");

        let interval = Duration::from_nanos(1_000_000_000 / self.params.sample_rate);
        if interval < MIN_SAMPLE_INTERVAL {
            return Err(anyhow!("sample rate too high"));
        } else if interval > MAX_SAMPLE_INTERVAL {
            return Err(anyhow!("sample rate too low"));
        }

        let duration = self.params.duration_ms.map(Duration::from_millis);

        let mut join_handle = self.join_handles.lock().unwrap();
        if join_handle.is_some() {
            return Err(anyhow!("already started"));
        }
        let mut handles = Vec::new();

        self.stop.store(false, Ordering::Relaxed);
        let self_clone = self.clone();
        handles.push(
            std::thread::Builder::new()
                .name(format!("{}: sampler", THREAD_NAME_TAG))
                .spawn(move || {
                    self_clone.sampler_loop(interval, duration).unwrap();
                })?,
        );

        *join_handle = Some(handles);
        Ok(())
    }

    fn sampler_loop(
        self: &Arc<Self>,
        interval: Duration,
        duration: Option<Duration>,
    ) -> anyhow::Result<()> {
        qos::set_thread_qos(QosClass::UserInteractive, None)?;
        set_realtime_scheduling(interval)?;

        // find "hv_vcpu_run" for guest stack sampling
        let mut symbolicator = DladdrSymbolicator::new()?;
        let hv_vcpu_run = symbolicator.symbol_range("hv_vcpu_run")?;

        let mut host_unwinder = FramePointerUnwinder {};

        let mut sample_batch_histogram = Histogram::<u64>::new(3)?;
        let mut thread_suspend_histogram = Histogram::<u64>::new(3)?;

        let mut threads = self.get_threads()?;

        let mut samples = SegVec::new();
        let mut resources = SegVec::new();
        let self_pid = std::process::id() as i32;
        let ktracer = Ktracer::start(&threads)?;

        let mut last_phys_footprint = memory::get_phys_footprint(self_pid)?;

        info!("started");

        let interval_mach = MachAbsoluteDuration::from_duration(interval);
        let wall_start_time = SystemTime::now();
        let profile_start_time = MachAbsoluteTime::now();
        let mut next_target_time = profile_start_time + interval_mach;
        let stop_time = duration
            .map(|d| profile_start_time + MachAbsoluteDuration::from_duration(d))
            .unwrap_or(MachAbsoluteTime::MAX);
        loop {
            // try to sample at a monotonic rate
            unsafe { check_mach!(mach_wait_until(next_target_time.0))? };
            next_target_time += interval_mach;

            let sample_batch_start = MachAbsoluteTime::now();

            // ingest queued vCPU samples
            while let Some(sample) = self.ingest_queue.pop() {
                samples.push(sample);
            }

            if sample_batch_start >= stop_time {
                break;
            }
            if self.stop.load(Ordering::Relaxed) {
                break;
            }

            for thread in &mut threads {
                // skip stopped threads
                if thread.stopped_at.is_some() {
                    continue;
                }

                match thread.sample(
                    &mut host_unwinder,
                    &mut thread_suspend_histogram,
                    &hv_vcpu_run,
                ) {
                    Ok(SampleResult::Sample(sample)) => {
                        samples.push(sample);
                    }
                    Ok(SampleResult::Queued) | Ok(SampleResult::ThreadStopped) => {}
                    Err(SampleError::ThreadSuspend(
                        MachError::InvalidArgument
                        | MachError::Terminated
                        | MachError::MachSendInvalidDest,
                    ))
                    | Err(SampleError::ThreadGetState(
                        MachError::InvalidArgument
                        | MachError::Terminated
                        | MachError::MachSendInvalidDest,
                    )) => {
                        // thread is gone
                        thread.stopped_at = Some(MachAbsoluteTime::now());
                    }
                    Err(e) => {
                        error!("failed to sample thread {:?}: {}", thread.id, e);
                        continue;
                    }
                };
            }

            // sample resources
            let resources_time = MachAbsoluteTime::now();
            let phys_footprint = memory::get_phys_footprint(self_pid)?;
            resources.push(ResourceSample {
                time: resources_time,
                phys_footprint: phys_footprint as i64 - last_phys_footprint as i64,
            });
            last_phys_footprint = phys_footprint;

            let sample_batch_end = MachAbsoluteTime::now();
            let sample_batch_duration = sample_batch_end - sample_batch_start;
            sample_batch_histogram.record(sample_batch_duration.nanos())?;

            if sample_batch_end > next_target_time {
                warn!(
                    "sample batch took too long: timer={:?} sampling={:?}",
                    sample_batch_start - (next_target_time - interval_mach),
                    sample_batch_duration
                );
                // prevent timer overshoot from accumulating
                next_target_time = sample_batch_end;
            }
        }

        let end_time = MachAbsoluteTime::now();
        let wall_end_time = SystemTime::now();

        // stop ktrace
        let ktrace_results = Some(ktracer.stop()?);

        // get symbolication context from one vCPU
        let guest_context = self.get_guest_context(&threads)?;

        // vCPU threads are no longer needed now that we've gotten guest context
        // tell them to drop VcpuProfilerState to avoid leaking memory
        let mut vcpu_agg_histograms = VcpuHistograms {
            sample_time: Histogram::<u64>::new(3)?,
            resume_and_sample: Histogram::<u64>::new(3)?,
        };

        for thread in &threads {
            if let Some(vcpu) = thread.vcpu.as_ref() {
                let (sender, receiver) = crossbeam::channel::bounded(1);
                vcpu.send_profiler_finish(sender);
                let results = receiver.recv()?;

                // aggregate vCPU histograms. per-vCPU is too much data to make sense of
                vcpu_agg_histograms
                    .sample_time
                    .add(results.histograms.sample_time)?;
                vcpu_agg_histograms
                    .resume_and_sample
                    .add(results.histograms.resume_and_sample)?;
            }
        }

        let results = ProfileResults {
            info: ProfileInfo {
                pid: std::process::id(),
                start_time: wall_start_time,
                start_time_abs: profile_start_time,
                end_time: wall_end_time,
                end_time_abs: end_time,
                params: self.params.clone(),
                num_samples: samples.len(),
            },
            samples,
            threads,
            resources,
            sample_batch_histogram,
            thread_suspend_histogram,
            vcpu_agg_histograms,
            ktrace: ktrace_results,
        };
        self.process_samples(guest_context, results)?;

        Ok(())
    }

    pub fn queue_sample(&self, partial: PartialSample) -> anyhow::Result<()> {
        self.ingest_queue
            .push(partial.sample)
            .map_err(|e| anyhow!("ingest queue full, dropping sample: {:?}", e))
    }

    fn get_threads(self: &Arc<Self>) -> anyhow::Result<Vec<ProfileeThread>> {
        let mut threads_list: thread_act_array_t = std::ptr::null_mut();
        let mut threads_count: mach_msg_type_number_t = 0;
        unsafe {
            check_mach!(task_threads(
                mach_task_self(),
                &mut threads_list,
                &mut threads_count,
            ))?
        };
        let threads_list = scopeguard::guard(threads_list, |p| unsafe {
            check_mach!(mach_vm_deallocate(
                mach_task_self(),
                p as mach_vm_address_t,
                (threads_count as usize * size_of::<thread_act_t>()) as mach_vm_size_t,
            ))
            .unwrap();
        });

        let mut threads = Vec::new();
        let thread_ports =
            unsafe { std::slice::from_raw_parts(*threads_list, threads_count as usize) };
        for &thread_port in thread_ports {
            match self.add_thread(&mut threads, thread_port) {
                Ok(()) => {}
                Err(e) => error!("failed to add thread: {}", e),
            }
        }

        Ok(threads)
    }

    fn add_thread(
        self: &Arc<Self>,
        threads: &mut Vec<ProfileeThread>,
        thread_port: thread_act_t,
    ) -> anyhow::Result<()> {
        // make sure we drop the port if this fails
        let thread_port = unsafe { MachPort::from_raw(thread_port) };

        let id = match ThreadId::from_port(&thread_port) {
            Ok(id) => id,
            Err(e) => {
                error!("failed to get thread ID: {}", e);
                return Ok(());
            }
        };

        let mut info: thread_extended_info = unsafe { std::mem::zeroed() };
        let mut info_count = THREAD_EXTENDED_INFO_COUNT;
        match unsafe {
            check_mach!(thread_info(
                thread_port.0,
                THREAD_EXTENDED_INFO as thread_flavor_t,
                &mut info as *mut _ as *mut _,
                &mut info_count,
            ))
        } {
            Ok(()) => {}
            Err(
                MachError::InvalidArgument | MachError::Terminated | MachError::MachSendInvalidDest,
            ) => {
                // thread is gone
                return Ok(());
            }
            Err(e) => return Err(e.into()),
        }

        let name_bytes: &[u8] = unsafe {
            std::slice::from_raw_parts(info.pth_name.as_ptr() as *const _, info.pth_name.len())
        };
        let name = CStr::from_bytes_until_nul(name_bytes)?
            .to_string_lossy()
            .to_string();

        // exclude profiler threads
        if name.contains(THREAD_NAME_TAG) {
            return Ok(());
        }

        let vcpu = if let Some(vcpu_id) = name.strip_prefix("vcpu") {
            if let Ok(vcpu_id) = vcpu_id.parse::<u8>() {
                self.vcpu_registry.get_vcpu(vcpu_id)
            } else {
                None
            }
        } else {
            None
        };

        if let Some(vcpu) = vcpu.as_ref() {
            let (sender, receiver) = crossbeam::channel::bounded(1);
            vcpu.send_profiler_init(ProfilerVcpuInit {
                profiler: self.clone(),
                completion_sender: sender,
            });

            // wait for init, so that vcpu init samples don't show up in the profile
            receiver.recv()?;
        }

        let option_name = if name.is_empty() { None } else { Some(name) };
        threads.push(ProfileeThread::new(id, thread_port, option_name, vcpu));

        Ok(())
    }

    fn get_guest_context(
        &self,
        threads: &[ProfileeThread],
    ) -> anyhow::Result<ProfilerGuestContext> {
        // to get a guest (Linux) symbolicator, ask one of the vCPUs to read the KASLR offset
        let vcpu: &Arc<VcpuHandleInner> = threads
            .iter()
            .find_map(|t| t.vcpu.as_ref())
            .ok_or_else(|| anyhow!("no vCPU threads found"))?;

        let (sender, receiver) = crossbeam::channel::bounded(1);
        vcpu.send_profiler_guest_fetch(sender);
        let response = receiver.recv()?;
        Ok(response)
    }

    fn process_samples(
        &self,
        mut guest_context: ProfilerGuestContext,
        mut prof: ProfileResults,
    ) -> anyhow::Result<()> {
        info!("processing samples");

        let threads_map = prof
            .threads
            .iter()
            .map(|t| (t.id, t))
            .collect::<AHashMap<_, _>>();

        // this also needs to be VecDeque to allow for fast push/pop/drain in StackTransforms
        let mut thread_states: AHashMap<ThreadId, ThreadFrameState> = prof
            .threads
            .iter()
            .map(|t| (t.id, ThreadFrameState::new()))
            .collect();

        let mut host_symbolicator = CachedSymbolicator::new(DladdrSymbolicator::new()?);
        let mut host_kernel_symbolicator = HostKernelSymbolicator::new()?;

        let transforms: Vec<Box<dyn StackTransform>> = vec![
            Box::new(CgoTransform {}),
            Box::new(LinuxIrqTransform {}),
            Box::new(LeafCallTransform {}),
            Box::new(HostSyscallTransform {}),
        ];

        let mut text_exporter = TextExporter::new(&prof.info, threads_map.clone())?;
        let mut ff_exporter = FirefoxExporter::new(&prof.info, threads_map)?;
        let mut total_bytes = 0;
        for sample in &mut prof.samples {
            total_bytes += size_of::<Sample>();

            let thread_state = thread_states
                .get_mut(&sample.thread_id)
                .expect("missing thread stack");

            // save last original stack for SameAsLast, before we potentially inject vmfaults
            if let SampleStack::Stack(stack) = &sample.stack {
                thread_state.last_orig_stack = Some(stack);
            }

            // to own a potential stack copy
            let mut _stack_copy: SampleStack;

            // this is bad, but fast and easy:
            // ktrace's vmfault injection is a special pre-symbolication transformation
            // needed because vmfaults are time-based, so samples with the same stack could have different stacks after vmfault injection
            let sample_stack = if let Some(kt_thread) = prof
                .ktrace
                .as_ref()
                .and_then(|r| r.threads.get(&sample.thread_id))
            {
                if kt_thread.is_time_in_fault(sample.sample_begin_time)
                    || kt_thread.is_time_in_fault(sample.time)
                {
                    // we were in a fault. let's inject a vmfault frame

                    // we always need to copy the stack for these:
                    // - if this sample is SameAsLast, we need to copy the last unsymbolicated stack
                    // - if this sample has a Stack, we need to copy it to modify it, because later samples might be SameAsLast but not in a fault
                    let mut new_stack = match &sample.stack {
                        SampleStack::Stack(stack) => stack.clone(),
                        SampleStack::SameAsLast => thread_state
                            .last_orig_stack
                            .expect("no last stack to copy")
                            .clone(),
                    };

                    // inject the frame
                    new_stack.push_front(Frame::new(
                        SampleCategory::HostKernel,
                        HostKernelSymbolicator::ADDR_VMFAULT,
                    ));

                    _stack_copy = SampleStack::Stack(new_stack);
                    &_stack_copy
                } else {
                    &sample.stack
                }
            } else {
                &sample.stack
            };

            let sframes = &mut thread_state.sframes_buf;
            match sample_stack {
                SampleStack::Stack(stack) => {
                    // symbolicate the frames
                    // reuse the Vec to avoid allocations
                    sframes.clear();
                    stack
                        .iter()
                        .map(|frame| {
                            let symbol = match frame.category {
                                SampleCategory::HostUserspace => {
                                    host_symbolicator.addr_to_symbol(frame.addr)
                                }
                                SampleCategory::GuestKernel => {
                                    match &mut guest_context.symbolicator {
                                        Some(s) => s.addr_to_symbol(frame.addr),
                                        None => Ok(None),
                                    }
                                }
                                SampleCategory::GuestUserspace => Ok(Some(SymbolResult {
                                    image: "guest".to_string(),
                                    image_base: 0,
                                    symbol_offset: Some(("<GUEST USERSPACE>".to_string(), 0)),
                                })),
                                SampleCategory::HostKernel => {
                                    host_kernel_symbolicator.addr_to_symbol(frame.addr)
                                }
                            }
                            .inspect_err(|e| {
                                error!("failed to symbolicate addr {:x}: {}", frame.addr, e)
                            })
                            .ok()
                            .flatten();

                            SymbolicatedFrame {
                                frame: *frame,
                                symbol,
                            }
                        })
                        .for_each(|sframe| sframes.push_back(sframe));

                    total_bytes += stack.capacity() * size_of::<Frame>();

                    // apply transforms
                    for transform in &transforms {
                        transform.transform(sframes)?;
                    }
                }

                // do nothing: we already symbolicated and transformed the last stack
                SampleStack::SameAsLast => {}
            }

            text_exporter.process_sample(sample, sframes)?;
            ff_exporter.process_sample(sample, sframes)?;
        }
        info!("writing to file: {}", self.params.output_path);
        text_exporter.write_to_path(&prof, &self.params.output_path)?;

        let ff_output_path = self.params.output_path.clone() + ".json";
        info!("writing to file: {}", &ff_output_path);
        if let Some(ktrace_results) = &prof.ktrace {
            ff_exporter.add_ktrace_markers(ktrace_results);
        }
        ff_exporter.add_resources(&prof.resources);
        ff_exporter.write_to_path(&prof, total_bytes, &ff_output_path)?;
        if let Err(e) = FirefoxApiServer::shared().add_and_open_profile(ff_output_path) {
            error!("failed to open in Firefox Profiler: {}", e);
        }

        Ok(())
    }

    pub fn stop(&self) -> anyhow::Result<()> {
        info!("stopping");
        let join_handles = self.join_handles.lock().unwrap().take();
        if let Some(join_handles) = join_handles {
            self.stop.store(true, Ordering::Relaxed);
            for handle in join_handles {
                handle.join().map_err(|e| anyhow!("join failed: {:?}", e))?;
            }
        } else {
            return Err(anyhow!("not running"));
        }

        *self.join_handles.lock().unwrap() = None;
        Ok(())
    }
}
