use std::{
    collections::HashMap,
    ffi::CStr,
    mem::size_of,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    thread::JoinHandle,
    time::Duration,
};

use anyhow::anyhow;
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
use symbolicator::{MacSymbolicator, Symbolicator};
use thread::{ProfileeThread, ThreadId};
use time::MachAbsoluteTime;
use tracing::{error, info};
use unwinder::FramehopUnwinder;
use utils::{
    qos::{self, QosClass},
    Mutex,
};

use crate::Parkable;

mod processor;
pub mod symbolicator;
mod thread;
mod time;
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

#[derive(Debug, Copy, Clone)]
enum Category {
    GuestUserspace,
    GuestKernel,
    HostUserspace,
    // TODO: how?
    //HostKernel,
}

#[derive(Debug, Clone)]
struct Sample {
    timestamp: MachAbsoluteTime,
    cpu_time: u64,

    thread_id: ThreadId,

    category: Category,
    stack: Vec<u64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProfilerParams {
    sample_rate: u64,
    output_path: String,
}

pub struct Profiler {
    parker: Arc<dyn Parkable>,
    params: ProfilerParams,

    stop: AtomicBool,
    join_handles: Mutex<Option<Vec<JoinHandle<()>>>>,

    samples: Mutex<Vec<Sample>>,
}

impl Profiler {
    pub fn new(params: ProfilerParams, parker: Arc<dyn Parkable>) -> Self {
        Self {
            parker,
            params,
            stop: AtomicBool::new(false),
            join_handles: Mutex::new(None),
            samples: Mutex::new(Vec::new()),
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

    fn sampler_loop(&self, interval: Duration) -> anyhow::Result<()> {
        qos::set_thread_qos(QosClass::UserInteractive, None)?;

        let threads = self.get_threads()?;
        info!("threads: {:?}", threads);

        // before we start, find "hv_vcpu_run" and "hv_trap"
        let symbolicator = MacSymbolicator {};
        let hv_vcpu_run = symbolicator.symbol_range("hv_vcpu_run")?;
        let hv_trap = symbolicator.symbol_range("hv_trap")?;
        info!("hv_vcpu_run: {:x?}", hv_vcpu_run);
        info!("hv_trap: {:x?}", hv_trap);

        let mut host_unwinder = FramehopUnwinder::new()?;

        loop {
            if self.stop.load(Ordering::Relaxed) {
                break;
            }

            // TODO: monotonic timer using absolute timeout or workgroup
            // TODO: throttle if falling behind
            std::thread::sleep(interval);

            for thread in &threads {
                // TODO: check if thread ran
                let (timestamp, stack) =
                    match thread.sample(&mut host_unwinder, &hv_vcpu_run, &hv_trap) {
                        Ok(r) => r,
                        Err(e) => {
                            error!("failed to sample thread {:?}: {}", thread.id(), e);
                            continue;
                        }
                    };
                let sample = Sample {
                    timestamp,
                    cpu_time: 0,

                    thread_id: thread.id(),

                    category: Category::HostUserspace,
                    stack,
                };
                self.add_sample(sample)?;
            }
        }

        self.stop.store(false, Ordering::Relaxed);
        self.process_samples(&threads)?;
        Ok(())
    }

    fn add_sample(&self, sample: Sample) -> anyhow::Result<()> {
        self.samples.lock().unwrap().push(sample);
        Ok(())
    }

    fn get_threads(&self) -> anyhow::Result<Vec<ProfileeThread>> {
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

            let vcpu_signal = if let Some(vcpu_id) = name.strip_prefix("vcpu") {
                if let Ok(vcpu_id) = vcpu_id.parse::<u8>() {
                    self.parker.get_vcpu(vcpu_id)
                } else {
                    None
                }
            } else {
                None
            };

            threads.push(ProfileeThread {
                port: thread_port,
                name,
                vcpu_signal,
            });
        }

        Ok(threads)
    }

    fn process_samples(&self, threads: &[ProfileeThread]) -> anyhow::Result<()> {
        let samples = self.samples.lock().unwrap();
        let threads_map = threads
            .iter()
            .map(|t| (t.id(), t))
            .collect::<HashMap<_, _>>();

        info!("processing samples");
        let mut processor = SampleProcessor::new(threads_map)?;
        for sample in &*samples {
            processor.process_sample(sample)?;
        }

        info!("writing to file: {}", self.params.output_path);
        processor.write_to_path(&self.params.output_path)?;
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
