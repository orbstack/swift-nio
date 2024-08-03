use std::{
    collections::{HashMap, VecDeque},
    ffi::CStr,
    mem::size_of,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    thread::JoinHandle,
    time::{Duration, SystemTime},
};

use anyhow::anyhow;
use crossbeam::queue::ArrayQueue;
use firefox::FirefoxSampleProcessor;
use libc::{
    thread_extended_info, thread_flavor_t, thread_info, THREAD_EXTENDED_INFO,
    THREAD_EXTENDED_INFO_COUNT,
};
use mach2::{
    mach_types::{thread_act_array_t, thread_act_t},
    message::mach_msg_type_number_t,
    task::task_threads,
    traps::mach_task_self,
    vm::mach_vm_deallocate,
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use processor::SampleProcessor;
use serde::{Deserialize, Serialize};
use symbolicator::{CachedSymbolicator, DladdrSymbolicator, LinuxSymbolicator, Symbolicator};
use thread::{ProfileeThread, SampleResult, ThreadId};
use time::MachAbsoluteTime;
use tracing::{error, info};
use transform::{CgoStackTransform, LinuxIrqStackTransform, StackTransform};
use unwinder::{FramePointerUnwinder, FramehopUnwinder};
use utils::{
    qos::{self, QosClass},
    Mutex,
};

use crate::{VcpuHandleInner, VcpuRegistry};

mod firefox;
mod processor;
pub mod symbolicator;
mod thread;
mod time;
mod transform;
mod unwinder;

pub use unwinder::STACK_DEPTH_LIMIT;

const MIN_SAMPLE_INTERVAL: Duration = Duration::from_micros(100);
const MAX_SAMPLE_INTERVAL: Duration = Duration::from_secs(2);

const THREAD_NAME_TAG: &str = "PROFILER";

// use a macro to preserve anyhow stack trace
#[macro_export]
macro_rules! check_mach {
    ($ret:expr) => {
        if $ret == mach2::kern_return::KERN_SUCCESS {
            Ok(())
        } else {
            Err(anyhow::anyhow!("mach error: {}", $ret))
        }
    };
}

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

#[derive(Debug, Clone)]
struct Sample {
    timestamp: MachAbsoluteTime,
    cpu_time_delta_us: u64,

    thread_id: ThreadId,

    stack: VecDeque<Frame>,
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

#[derive(Clone)]
pub struct PartialSample {
    inner: Sample,
    profiler: Arc<Profiler>,
}

impl PartialSample {
    pub fn finish(self) -> anyhow::Result<()> {
        self.profiler.add_sample(self.inner)
    }

    pub fn prepend_stack(&mut self, frame: Frame) {
        self.inner.stack.push_front(frame);
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

    samples: Mutex<Vec<Sample>>,
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
            samples: Mutex::new(Vec::new()),
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

        // before we start, find "hv_vcpu_run" and "hv_trap"
        let symbolicator = DladdrSymbolicator::new()?;
        let hv_vcpu_run = symbolicator.symbol_range("hv_vcpu_run")?;
        let hv_trap = symbolicator.symbol_range("hv_trap")?;
        info!("hv_vcpu_run: {:x?}", hv_vcpu_run);
        info!("hv_trap: {:x?}", hv_trap);

        let mut host_unwinder = FramePointerUnwinder {};

        let wall_start_time = SystemTime::now();
        let start_time = MachAbsoluteTime::now();
        let mut threads = self.get_threads(start_time)?;

        loop {
            // TODO: monotonic timer using absolute timeout or workgroup
            // TODO: throttle if falling behind
            std::thread::sleep(interval);

            // ingest samples
            while let Some(sample) = self.ingest_queue.pop() {
                self.add_sample(sample)?;
            }

            if self.stop.load(Ordering::Relaxed) {
                break;
            }

            for thread in &mut threads {
                // skip stopped threads
                if thread.stopped_at.is_some() {
                    continue;
                }

                // TODO: skip if 0 (after profiling and optimizing)
                // TODO: handle stopped threads
                let cpu_time_delta_us = match thread.cpu_time_delta_us() {
                    Ok(delta) => delta,
                    Err(e) => {
                        error!("failed to get cpu time for thread {:?}: {}", thread.id(), e);
                        continue;
                    }
                };

                match thread.sample(self, &mut host_unwinder, &hv_vcpu_run, &hv_trap) {
                    Ok(SampleResult::Sample(mut sample)) => {
                        sample.cpu_time_delta_us = cpu_time_delta_us;
                        self.add_sample(sample)?;
                    }
                    Ok(SampleResult::Queued) => {}
                    Err(e) => {
                        error!("failed to sample thread {:?}: {}", thread.id(), e);
                        continue;
                    }
                };
            }
        }

        let end_time = MachAbsoluteTime::now();
        let wall_end_time = SystemTime::now();

        let info = ProfileInfo {
            pid: unsafe { libc::getpid() },
            start_time: wall_start_time,
            start_time_abs: start_time,
            end_time: wall_end_time,
            end_time_abs: end_time,
            params: self.params.clone(),
        };

        self.stop.store(false, Ordering::Relaxed);
        self.process_samples(&info, &threads)?;
        Ok(())
    }

    fn add_sample(&self, sample: Sample) -> anyhow::Result<()> {
        self.samples.lock().unwrap().push(sample);
        Ok(())
    }

    fn get_threads(&self, now: MachAbsoluteTime) -> anyhow::Result<Vec<ProfileeThread>> {
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
            let mut info: thread_extended_info = unsafe { std::mem::zeroed() };
            let mut info_count = THREAD_EXTENDED_INFO_COUNT;
            unsafe {
                check_mach!(thread_info(
                    thread_port,
                    THREAD_EXTENDED_INFO as thread_flavor_t,
                    &mut info as *mut _ as *mut _,
                    &mut info_count,
                ))?
            };

            let name_bytes: &[u8] = unsafe {
                std::slice::from_raw_parts(info.pth_name.as_ptr() as *const _, info.pth_name.len())
            };
            let name = CStr::from_bytes_until_nul(name_bytes)?
                .to_string_lossy()
                .to_string();

            // exclude profiler threads
            if name.contains(THREAD_NAME_TAG) {
                continue;
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

            threads.push(ProfileeThread {
                port: thread_port,
                name,
                vcpu,

                last_cpu_time_us: None,

                added_at: now,
                stopped_at: None,
            });
        }

        Ok(threads)
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
        info: &ProfileInfo,
        threads: &[ProfileeThread],
    ) -> anyhow::Result<()> {
        info!("processing samples");

        let mut samples = self.samples.lock().unwrap();
        let threads_map = threads
            .iter()
            .map(|t| (t.id(), t))
            .collect::<HashMap<_, _>>();

        let guest_context = self.get_guest_context(threads)?;

        // post-process the stack
        let host_symbolicator = CachedSymbolicator::new(DladdrSymbolicator::new()?);
        let cgo_transform = CgoStackTransform::new(&host_symbolicator);
        for sample in &mut *samples {
            cgo_transform.transform(&mut sample.stack)?;
        }
        if let Some(guest_symbolicator) = guest_context.symbolicator.as_ref() {
            let irq_transform = LinuxIrqStackTransform::new(&host_symbolicator, guest_symbolicator);
            for sample in &mut *samples {
                irq_transform.transform(&mut sample.stack)?;
            }

            // let leaf_transform = LeafCallTransform::new(&host_symbolicator, guest_symbolicator);
            // for sample in &mut *samples {
            //     leaf_transform.transform(&mut sample.stack)?;
            // }
        }

        let mut processor = SampleProcessor::new(
            info,
            threads_map.clone(),
            &host_symbolicator,
            guest_context.symbolicator.as_ref(),
        )?;
        for sample in &*samples {
            processor.process_sample(sample)?;
        }
        info!("writing to file: {}", self.params.output_path);
        processor.write_to_path(&self.params.output_path)?;

        let mut processor = FirefoxSampleProcessor::new(
            info,
            threads_map,
            &host_symbolicator,
            guest_context.symbolicator.as_ref(),
        )?;
        for sample in &*samples {
            processor.process_sample(sample)?;
        }
        info!("writing to file: {}.json", self.params.output_path);
        processor.write_to_path(&(self.params.output_path.clone() + ".json"))?;

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
