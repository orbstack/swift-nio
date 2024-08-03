use std::{
    collections::VecDeque,
    ffi::CStr,
    mem::size_of,
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc::Sender,
        Arc,
    },
    thread::JoinHandle,
    time::{Duration, SystemTime},
};

use ahash::AHashMap;
use anyhow::anyhow;
use buffer::SegVec;
use crossbeam::queue::ArrayQueue;
use firefox::FirefoxSampleProcessor;
use hdrhistogram::Histogram;
use libc::{
    thread_extended_info, thread_flavor_t, thread_info, THREAD_EXTENDED_INFO,
    THREAD_EXTENDED_INFO_COUNT,
};
use mach2::{
    kern_return::kern_return_t,
    mach_port::mach_port_deallocate,
    mach_time::mach_wait_until,
    mach_types::{thread_act_array_t, thread_act_t},
    message::{mach_msg_type_number_t, MACH_SEND_INVALID_DEST},
    task::task_threads,
    traps::mach_task_self,
    vm::mach_vm_deallocate,
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use processor::TextSampleProcessor;
use sched::set_realtime_scheduling;
use serde::{Deserialize, Serialize};
use server::FirefoxApiServer;
use stats::dump_histogram;
use symbolicator::{
    CachedSymbolicator, DladdrSymbolicator, LinuxSymbolicator, SymbolResult, Symbolicator,
};
use thread::{ProfileeThread, SampleError, SampleResult, ThreadId};
use time::{MachAbsoluteDuration, MachAbsoluteTime};
use tracing::{error, info};
use transform::{CgoStackTransform, LeafCallTransform, LinuxIrqStackTransform, StackTransform};
use unwinder::FramePointerUnwinder;
use utils::{
    qos::{self, QosClass},
    Mutex,
};

use crate::{VcpuHandleInner, VcpuRegistry};

mod buffer;
mod firefox;
mod ktrace;
mod processor;
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
    #[error("MACH_SEND_INVALID_DEST")]
    MachSendInvalidDest,
    #[error("mach error: {0}")]
    Other(kern_return_t),
}

impl MachError {
    fn from_ret(ret: kern_return_t) -> Self {
        match ret {
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
    timestamp: MachAbsoluteTime,
    cpu_time_delta_us: u32,
    thread_id: ThreadId,
    stack: SampleStack,
}

#[derive(Debug)]
enum SampleStack {
    Stack(VecDeque<Frame>),
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

#[derive(Debug, Clone)]
pub(crate) struct SymbolicatedFrame {
    frame: Frame,
    symbol: Option<SymbolResult>,
}

pub struct PartialSample {
    sample: Sample,
}

impl PartialSample {
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
    output_path: String,
}

#[derive(Clone)]
pub struct ProfilerGuestContext {
    pub symbolicator: Option<LinuxSymbolicator>,
}

#[derive(Clone)]
pub struct ProfilerVcpuInit {
    pub profiler: Arc<Profiler>,
    pub completion_sender: Sender<()>,
}

#[derive(Debug, Clone)]
pub struct ProfileInfo {
    pub pid: i32,
    pub start_time: SystemTime,
    pub start_time_abs: MachAbsoluteTime,
    pub end_time: SystemTime,
    pub end_time_abs: MachAbsoluteTime,
    pub params: ProfilerParams,
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
        let interval = Duration::from_nanos(1_000_000_000 / self.params.sample_rate);
        if interval < MIN_SAMPLE_INTERVAL {
            return Err(anyhow!("sample rate too high"));
        } else if interval > MAX_SAMPLE_INTERVAL {
            return Err(anyhow!("sample rate too low"));
        }

        let mut join_handle = self.join_handles.lock().unwrap();
        if join_handle.is_some() {
            return Err(anyhow!("already started"));
        }
        let mut handles = Vec::new();

        let self_clone = self.clone();
        handles.push(
            std::thread::Builder::new()
                .name(format!("{}: sampler", THREAD_NAME_TAG))
                .spawn(move || {
                    self_clone.sampler_loop(interval).unwrap();
                })?,
        );

        *join_handle = Some(handles);
        Ok(())
    }

    fn sampler_loop(self: &Arc<Self>, interval: Duration) -> anyhow::Result<()> {
        qos::set_thread_qos(QosClass::UserInteractive, None)?;
        set_realtime_scheduling(interval)?;

        // before we start, find "hv_vcpu_run" and "hv_trap"
        let mut symbolicator = DladdrSymbolicator::new()?;
        let hv_vcpu_run = symbolicator.symbol_range("hv_vcpu_run")?;
        let hv_trap = symbolicator.symbol_range("hv_trap")?;
        info!("hv_vcpu_run: {:x?}", hv_vcpu_run);
        info!("hv_trap: {:x?}", hv_trap);

        let mut host_unwinder = FramePointerUnwinder {};

        let mut sample_batch_histogram = Histogram::<u64>::new(3)?;
        let mut thread_suspend_histogram = Histogram::<u64>::new(3)?;

        // must be before we start adding threads, to avoid overflow
        let profile_start_time = MachAbsoluteTime::now();
        let wall_start_time = SystemTime::now();
        let mut threads = self.get_threads()?;

        let mut samples = SegVec::new();

        // get time again before starting the loop
        let interval_mach = MachAbsoluteDuration::from_duration(interval);
        let mut next_target_time = MachAbsoluteTime::now() + interval_mach;
        loop {
            // try to sample at a monotonic rate
            unsafe { check_mach!(mach_wait_until(next_target_time.0))? };
            next_target_time += interval_mach;

            let sample_batch_start = MachAbsoluteTime::now();

            // ingest queued vCPU samples
            while let Some(sample) = self.ingest_queue.pop() {
                samples.push(sample);
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
                    &hv_trap,
                ) {
                    Ok(SampleResult::Sample(sample)) => {
                        samples.push(sample);
                    }
                    Ok(SampleResult::Queued) | Ok(SampleResult::ThreadStopped) => {}
                    Err(SampleError::ThreadSuspend(MachError::MachSendInvalidDest))
                    | Err(SampleError::ThreadGetState(MachError::MachSendInvalidDest)) => {
                        // thread is gone
                        thread.stopped_at = Some(MachAbsoluteTime::now());
                    }
                    Err(e) => {
                        error!("failed to sample thread {:?}: {}", thread.id(), e);
                        continue;
                    }
                };
            }

            let sample_batch_end = MachAbsoluteTime::now();
            let sample_batch_duration = sample_batch_end - sample_batch_start;
            sample_batch_histogram.record(sample_batch_duration.nanos())?;

            if sample_batch_end > next_target_time {
                error!(
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

        dump_histogram("sample batch time", &sample_batch_histogram);
        dump_histogram("thread suspend time", &thread_suspend_histogram);

        let info = ProfileInfo {
            pid: unsafe { libc::getpid() },
            start_time: wall_start_time,
            start_time_abs: profile_start_time,
            end_time: wall_end_time,
            end_time_abs: end_time,
            params: self.params.clone(),
        };

        self.stop.store(false, Ordering::Relaxed);
        self.process_samples(samples, &info, &threads)?;
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
        let thread_port = scopeguard::guard(thread_port, |p| unsafe {
            check_mach!(mach_port_deallocate(mach_task_self(), p)).unwrap();
        });

        let mut info: thread_extended_info = unsafe { std::mem::zeroed() };
        let mut info_count = THREAD_EXTENDED_INFO_COUNT;
        match unsafe {
            check_mach!(thread_info(
                *thread_port,
                THREAD_EXTENDED_INFO as thread_flavor_t,
                &mut info as *mut _ as *mut _,
                &mut info_count,
            ))
        } {
            Ok(()) => {}
            Err(MachError::MachSendInvalidDest) => {
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
            let (sender, receiver) = std::sync::mpsc::channel();
            vcpu.send_profiler_init(ProfilerVcpuInit {
                profiler: self.clone(),
                completion_sender: sender,
            });
            receiver.recv()?;
        }

        threads.push(ProfileeThread::new(*thread_port, name, vcpu));

        // we've added it, so now the port is owned by ProfileeThread
        std::mem::forget(thread_port);

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

        let (sender, receiver) = std::sync::mpsc::channel();
        vcpu.send_profiler_guest_fetch(sender);
        let response = receiver.recv()?;
        Ok(response)
    }

    fn process_samples(
        &self,
        mut samples: SegVec<Sample, SEGMENT_SIZE>,
        info: &ProfileInfo,
        threads: &[ProfileeThread],
    ) -> anyhow::Result<()> {
        info!("processing samples");

        let threads_map = threads
            .iter()
            .map(|t| (t.id(), t))
            .collect::<AHashMap<_, _>>();

        let mut last_thread_stacks: AHashMap<ThreadId, Vec<SymbolicatedFrame>> = threads
            .iter()
            .map(|t| (t.id(), Vec::with_capacity(STACK_DEPTH_LIMIT)))
            .collect();
        let mut guest_context = self.get_guest_context(threads)?;

        let mut host_symbolicator = CachedSymbolicator::new(DladdrSymbolicator::new()?);

        let cgo_transform = CgoStackTransform {};
        let irq_transform = LinuxIrqStackTransform {};
        let leaf_transform = LeafCallTransform {};

        let mut text_processor = TextSampleProcessor::new(info, threads_map.clone())?;
        let mut ff_processor = FirefoxSampleProcessor::new(info, threads_map)?;
        for sample in &mut samples {
            let sframes = last_thread_stacks
                .get_mut(&sample.thread_id)
                .expect("missing thread stack");
            match &sample.stack {
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
                                _ => Ok(None),
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
                        .for_each(|sframe| sframes.push(sframe));
                }
                // do nothing: we already symbolicated the last stack
                SampleStack::SameAsLast => {}
            }

            // apply transforms
            cgo_transform.transform(sframes)?;
            irq_transform.transform(sframes)?;
            leaf_transform.transform(sframes)?;

            text_processor.process_sample(sample, sframes)?;
            ff_processor.process_sample(sample, sframes)?;
        }
        info!("writing to file: {}", self.params.output_path);
        text_processor.write_to_path(&self.params.output_path)?;

        let ff_output_path = self.params.output_path.clone() + ".json";
        info!("writing to file: {}", &ff_output_path);
        ff_processor.write_to_path(&ff_output_path)?;
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
