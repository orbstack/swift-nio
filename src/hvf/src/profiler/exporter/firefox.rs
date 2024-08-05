use ahash::AHashMap;
use anyhow::anyhow;
use serde::Serialize;
use std::collections::VecDeque;
use std::hash::Hash;
use std::io::BufWriter;
use std::ops::Sub;
use std::thread::available_parallelism;
use std::time::SystemTime;
use std::{collections::HashMap, fs::File};
use tracing::error;

use crate::profiler::buffer::SegVec;
use crate::profiler::ktrace::KtraceResults;
use crate::profiler::memory::system_total_memory;
use crate::profiler::sched::sysctl_string;
use crate::profiler::{
    thread::{ProfileeThread, ThreadId},
    Sample,
};
use crate::profiler::{
    Frame, FrameCategory, ProfileInfo, ProfileResults, ResourceSample, SymbolicatedFrame,
};

#[derive(Serialize, Debug, Clone, PartialEq, Eq, Hash)]
#[serde(transparent)]
struct Pid(String);

impl From<u32> for Pid {
    fn from(pid: u32) -> Self {
        Pid(pid.to_string())
    }
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct FirefoxProfile<'a> {
    meta: ProfileMeta<'a>,
    libs: &'a [Lib],
    counters: Vec<FirefoxCounter>,
    profiler_overhead: Vec<ProfilerOverhead>,
    threads: Vec<FirefoxThread<'a>>,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
#[allow(unused)]
enum GraphColor {
    Blue,
    Green,
    Grey,
    Ink,
    Magenta,
    Orange,
    Purple,
    Red,
    Teal,
    Yellow,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct FirefoxCounter {
    name: String,
    category: String,
    description: String,
    color: Option<GraphColor>,
    pid: Pid,
    main_thread_index: usize,
    samples: FirefoxCounterSampleTable,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct ProfileMeta<'a> {
    interval: Milliseconds,
    start_time: Milliseconds,
    end_time: Option<Milliseconds>,
    profiling_start_time: Option<Milliseconds>,
    profiling_end_time: Option<Milliseconds>,
    process_type: u32,
    extensions: ExtensionTable,
    categories: &'a [FirefoxCategory],
    product: String,
    stackwalk: u8,
    debug: bool,
    version: u32,
    preprocessed_profile_version: u32,

    sample_units: SampleUnits,
    imported_from: String,

    main_memory: Option<usize>,
    oscpu: Option<String>,
    #[serde(rename = "CPUName")]
    cpu_name: Option<String>,
    #[serde(rename = "logicalCPUs")]
    logical_cpus: Option<usize>,
    misc: Option<String>,
    #[serde(rename = "appBuildID")]
    app_build_id: Option<String>,
    #[serde(rename = "sourceURL")]
    source_url: Option<String>,

    symbolicated: bool,
    uses_only_one_stack_type: bool,
    does_not_use_frame_implementation: bool,
    source_code_is_not_on_searchfox: bool,

    marker_schema: Vec<Todo>,
    configuration: Option<ProfilerConfiguration>,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct ProfilerConfiguration {
    threads: Vec<String>,
    features: Vec<String>,
    capacity: usize,       // 8-byte units
    duration: Option<u64>, // seconds
}

macro_rules! table_type {
    ($base_type:ident$(<$base_lt:lifetime>)? {$(
        $(#[$attr:meta])*
        $vis:vis $name:ident : $ty:ty,
    )*}, $table_type:ident$(<$table_lt:lifetime>)?$( {
        $($extra_vis:vis $extra_name:ident : $extra_ty:ty,)*
    })?) => {
        struct $base_type$(<$base_lt>)? {
            $(
                $vis $name: $ty,
            )*
        }

        #[derive(Serialize, Default, Debug, Clone)]
        #[serde(rename_all = "camelCase")]
        struct $table_type$(<$table_lt>)? {
            length: usize,
            $(
                $(#[$attr])*
                $vis $name: Vec<$ty>,
            )*
            $(
                $($extra_vis $extra_name: $extra_ty,)*
            )?
        }

        impl$(<$table_lt>)? $table_type$(<$table_lt>)? {
            // might not be used for all types
            #[allow(dead_code)]
            fn push(&mut self, value: $base_type$(<$table_lt>)?) {
                self.length += 1;
                $(
                    self.$name.push(value.$name);
                )*
            }
        }

        impl<$($table_lt,)? _KeyType> From<&KeyedTable<_KeyType, $base_type$(<$table_lt>)?>> for $table_type$(<$table_lt>)? {
            fn from(keyed: &KeyedTable<_KeyType, $base_type$(<$table_lt>)?>) -> Self {
                // ..Default::default() is only for structs with extra table fields
                #[allow(clippy::needless_update)]
                let mut table = Self {
                    length: keyed.values.len(),
                    $(
                        $name: Vec::with_capacity(keyed.values.len()),
                    )*
                    ..Default::default()
                };

                for value in &keyed.values {
                    $(
                        table.$name.push(value.$name.clone());
                    )*
                }

                table
            }
        }
    }
}

table_type!(
    Extension {
        #[serde(rename = "baseURL")]
        base_url: String,
        id: String,
        name: String,
    },
    ExtensionTable
);

#[derive(Serialize, Debug, Clone)]
#[allow(unused)]
enum ThreadCPUDeltaUnit {
    #[serde(rename = "ns")]
    Nanoseconds,
    #[serde(rename = "µs")]
    Microseconds,
    #[serde(rename = "variable CPU cycles")]
    VariableCPUCycles,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct SampleUnits {
    time: String,        // "ms"
    event_delay: String, // "ms"
    #[serde(rename = "threadCPUDelta")]
    thread_cpu_delta: ThreadCPUDeltaUnit,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct Lib {
    arch: String,
    name: String,
    path: String,
    debug_name: String,
    debug_path: String,
    breakpad_id: String,
    code_id: Option<String>,
}

#[derive(Serialize, Debug, Copy, Clone)]
#[serde(transparent)]
struct Milliseconds(f64);

impl Sub for Milliseconds {
    type Output = Milliseconds;

    fn sub(self, rhs: Self) -> Self::Output {
        Milliseconds(self.0 - rhs.0)
    }
}

#[derive(Serialize, Debug, Copy, Clone, Default)]
#[serde(transparent)]
struct Microseconds(f64);

table_type!(
    FirefoxCounterSample {
        time: Milliseconds,
        // number of times 'count' was changed since previous sample
        number: Option<u64>,
        // value
        count: i64,
    },
    FirefoxCounterSampleTable
);

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct FirefoxCategory {
    name: String,
    color: String,
    subcategories: Vec<String>,
}

table_type!(
    ProfilerOverheadSample {
        counters: Microseconds,
        expired_marker_cleaning: Microseconds,
        locking: Microseconds,
        threads: Microseconds,
        time: Milliseconds,
    },
    ProfilerOverheadSampleTable
);

#[derive(Serialize, Debug, Clone, Default)]
#[serde(rename_all = "camelCase")]
struct ProfilerOverheadStats {
    max_cleaning: Microseconds,
    max_counter: Microseconds,
    max_interval: Microseconds,
    max_lockings: Microseconds,
    max_overhead: Microseconds,
    max_thread: Microseconds,
    mean_cleaning: Microseconds,
    mean_counter: Microseconds,
    mean_interval: Microseconds,
    mean_lockings: Microseconds,
    mean_overhead: Microseconds,
    mean_thread: Microseconds,
    min_cleaning: Microseconds,
    min_counter: Microseconds,
    min_interval: Microseconds,
    min_lockings: Microseconds,
    min_overhead: Microseconds,
    min_thread: Microseconds,
    overhead_durations: Microseconds,
    overhead_percentage: Microseconds,
    profiled_duration: Microseconds,
    sampling_count: usize,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct ProfilerOverhead {
    samples: ProfilerOverheadSampleTable,
    statistics: Option<ProfilerOverheadStats>,
    pid: Pid,
    main_thread_index: usize,
}

type Tid = u64;

type Todo = ();

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct FirefoxThread<'a> {
    process_type: String,
    process_startup_time: Milliseconds,
    process_shutdown_time: Option<Milliseconds>,
    register_time: Milliseconds,
    unregister_time: Option<Milliseconds>,
    paused_ranges: Vec<Todo>,
    show_markers_in_timeline: Option<bool>,
    name: String,
    is_main_thread: bool,
    process_name: String,
    pid: Pid,
    tid: Tid,
    samples: FirefoxSampleTable,
    markers: RawMarkerTable,
    stack_table: FirefoxStackTable,
    frame_table: FirefoxFrameTable,
    string_array: &'a [String],
    func_table: FuncTable,
    resource_table: ResourceTable,
    native_symbols: NativeSymbolTable,
}

type IndexIntoStackTable = usize;
type IndexIntoStringTable = usize;
type IndexIntoCategoryList = usize;
type IndexIntoFrameTable = usize;
type IndexIntoSubcategoryListForCategory = usize;
type IndexIntoFuncTable = usize;
type IndexIntoNativeSymbolTable = usize;
type IndexIntoResourceTable = isize; // can be -1
type IndexIntoLibs = usize;
type ResourceTypeEnum = u32;

#[derive(Serialize, Debug, Clone, Default)]
#[allow(unused)]
enum WeightType {
    #[serde(rename = "samples")]
    #[default]
    Samples,
    #[serde(rename = "tracing-ms")]
    TracingMs,
    #[serde(rename = "bytes")]
    Bytes,
}

table_type!(FirefoxSample {
    stack: Option<IndexIntoStackTable>,
    time: Milliseconds,
    weight: f64,
    #[serde(rename = "threadCPUDelta")]
    thread_cpu_delta: Microseconds,
}, FirefoxSampleTable {
    weight_type: WeightType,
});

// serde can't serialize enums as integer tags
#[derive(Serialize, Debug, Clone)]
#[serde(transparent)]
struct MarkerPhase(u16);

#[allow(unused)]
impl MarkerPhase {
    pub const INSTANT: MarkerPhase = MarkerPhase(0);
    pub const INTERVAL: MarkerPhase = MarkerPhase(1);
    pub const INTERVAL_START: MarkerPhase = MarkerPhase(2);
    pub const INTERVAL_END: MarkerPhase = MarkerPhase(3);
}

table_type!(RawMarker {
    data: Option<Todo>,
    name: IndexIntoStringTable,
    start_time: Option<Milliseconds>,
    end_time: Option<Milliseconds>,
    phase: MarkerPhase,
    category: IndexIntoCategoryList,
    thread_id: Option<Tid>,
}, RawMarkerTable);

table_type!(FirefoxStack {
    frame: IndexIntoFrameTable,
    category: IndexIntoCategoryList,
    subcategory: IndexIntoSubcategoryListForCategory,
    prefix: Option<IndexIntoStackTable>,
}, FirefoxStackTable);

table_type!(FirefoxFrame {
    address: u64,
    inline_depth: u32,
    category: Option<IndexIntoCategoryList>,
    subcategory: Option<IndexIntoSubcategoryListForCategory>,
    func: IndexIntoFuncTable,
    native_symbol: Option<IndexIntoNativeSymbolTable>,
    #[serde(rename = "innerWindowID")]
    inner_window_id: Option<u64>,
    implementation: Option<IndexIntoStringTable>,
    line: Option<u32>,
    column: Option<u32>,
}, FirefoxFrameTable);

table_type!(Func {
    name: IndexIntoStringTable,

    #[serde(rename = "isJS")]
    is_js: bool,
    #[serde(rename = "relevantForJS")]
    relevant_for_js: bool,

    resource: IndexIntoResourceTable,

    file_name: Option<IndexIntoStringTable>,
    line_number: Option<u32>,
    column_number: Option<u32>,
}, FuncTable);

table_type!(Resource {
    lib: Option<IndexIntoLibs>,
    name: IndexIntoStringTable,
    host: Option<IndexIntoStringTable>,
    #[serde(rename = "type")]
    type_: ResourceTypeEnum,
}, ResourceTable);

table_type!(NativeSymbol {
    lib_index: IndexIntoLibs,
    address: u64,
    name: IndexIntoStringTable,
    function_size: Option<usize>,
}, NativeSymbolTable);

struct KeyedTable<K, V> {
    values: Vec<V>,
    keys: HashMap<K, usize>,
}

impl<K: Eq + Hash, V> KeyedTable<K, V> {
    fn new() -> Self {
        Self {
            values: Vec::new(),
            keys: HashMap::new(),
        }
    }

    fn force_insert(&mut self, key: K, value: V) -> usize {
        let index = self.values.len();
        self.keys.insert(key, index);
        self.values.push(value);
        index
    }

    fn get(&self, key: &K) -> Option<(&V, usize)> {
        self.keys
            .get(key)
            .map(|&index| (&self.values[index], index))
    }

    fn get_or_insert(&mut self, key: K, value: V) -> (&V, usize) {
        self.get_or_insert_with(key, || value)
    }

    fn get_or_insert_with(&mut self, key: K, f: impl FnOnce() -> V) -> (&V, usize) {
        if let Some(&index) = self.keys.get(&key) {
            (&self.values[index], index)
        } else {
            let value = f();
            let index = self.force_insert(key, value);
            (&self.values[index], index)
        }
    }
}

impl<K: Eq + Hash, V> Default for KeyedTable<K, V> {
    fn default() -> Self {
        Self::new()
    }
}

impl KeyedTable<String, String> {
    fn get_or_insert_str(&mut self, key: &str) -> usize {
        if let Some(&index) = self.keys.get(key) {
            index
        } else {
            let index = self.values.len();
            self.keys.insert(key.to_string(), index);
            self.values.push(key.to_string());
            index
        }
    }
}

type SymbolKey = (String, String);

struct ThreadState<'a> {
    thread: &'a ProfileeThread,
    resources: KeyedTable<String, Resource>,
    frames: KeyedTable<Frame, FirefoxFrame>,
    samples: FirefoxSampleTable,
    strings: KeyedTable<String, String>,
    funcs: KeyedTable<SymbolKey, Func>,
    native_symbols: KeyedTable<SymbolKey, NativeSymbol>,
    stacks: KeyedTable<Vec<(IndexIntoCategoryList, IndexIntoFrameTable)>, FirefoxStack>,
    raw_markers: KeyedTable<(), RawMarker>,
}

impl ThreadState<'_> {
    fn insert_stack(
        &mut self,
        stack: &[(IndexIntoCategoryList, IndexIntoFrameTable)],
    ) -> Option<IndexIntoStackTable> {
        if stack.is_empty() {
            return None;
        }

        let stack_vec = stack.to_vec();
        if let Some((_, index)) = self.stacks.get(&stack_vec) {
            return Some(index);
        }

        let (category_i, frame_i) = stack[0];
        let prefix_frames = &stack[1..];
        let ff_stack = FirefoxStack {
            frame: frame_i,
            category: category_i,
            subcategory: 0,
            prefix: self.insert_stack(prefix_frames),
        };
        Some(self.stacks.get_or_insert(stack_vec, ff_stack).1)
    }
}

fn image_basename(image: &str) -> &str {
    image.rsplit('/').next().unwrap_or(image)
}

pub struct FirefoxExporter<'a> {
    info: &'a ProfileInfo,
    threads: AHashMap<ThreadId, ThreadState<'a>>,

    counters: Vec<FirefoxCounter>,

    categories: KeyedTable<FrameCategory, FirefoxCategory>,
    libs: KeyedTable<String, Lib>,
}

impl<'a> FirefoxExporter<'a> {
    pub fn new(
        info: &'a ProfileInfo,
        threads_map: &'a AHashMap<ThreadId, &'a ProfileeThread>,
    ) -> anyhow::Result<Self> {
        let mut categories = KeyedTable::new();
        categories.get_or_insert(
            FrameCategory::GuestUserspace,
            FirefoxCategory {
                name: "Guest Userspace".to_string(),
                color: "grey".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );
        categories.get_or_insert(
            FrameCategory::GuestKernel,
            FirefoxCategory {
                name: "Guest Kernel".to_string(),
                color: "green".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );
        categories.get_or_insert(
            FrameCategory::HostUserspace,
            FirefoxCategory {
                name: "Host Userspace".to_string(),
                color: "yellow".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );
        categories.get_or_insert(
            FrameCategory::HostKernel,
            FirefoxCategory {
                name: "Host Kernel".to_string(),
                color: "blue".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );

        Ok(Self {
            info,
            threads: threads_map
                .iter()
                .map(|(&id, &thread)| {
                    (
                        id,
                        ThreadState {
                            thread,
                            resources: KeyedTable::new(),
                            samples: FirefoxSampleTable::default(),
                            strings: KeyedTable::new(),
                            frames: KeyedTable::new(),
                            funcs: KeyedTable::new(),
                            native_symbols: KeyedTable::new(),
                            stacks: KeyedTable::new(),
                            raw_markers: KeyedTable::new(),
                        },
                    )
                })
                .collect(),

            counters: Vec::new(),

            categories,
            libs: KeyedTable::new(),
        })
    }

    pub fn process_sample(
        &mut self,
        sample: &Sample,
        sframes: &VecDeque<SymbolicatedFrame>,
    ) -> anyhow::Result<()> {
        let thread = self
            .threads
            .get_mut(&sample.thread_id)
            .ok_or_else(|| anyhow!("thread not found: {:?}", sample.thread_id))?;

        let stack_frames = sframes
            .iter()
            .map(|sframe| {
                let frame = sframe.frame;

                // find frame for this (category, addr)
                let ff_frame = thread.frames.get_or_insert_with(frame, || {
                    let lib_path = sframe.symbol
                        .as_ref()
                        .map(|s| s.image.clone())
                        .unwrap_or_else(|| "<unknown>".to_string());

                    let lib = self.libs.get_or_insert_with(lib_path.clone(), || Lib {
                        arch: "arm64".to_string(),
                        name: image_basename(&lib_path).to_string(),
                        path: lib_path.clone(),
                        debug_name: image_basename(&lib_path).to_string(),
                        debug_path: lib_path.clone(),
                        // TODO: hash?
                        breakpad_id: lib_path.clone(),
                        code_id: None,
                    });

                    let resource =
                        thread
                            .resources
                            .get_or_insert_with(lib_path.clone(), || Resource {
                                name: thread.strings.get_or_insert_str(image_basename(&lib_path)),
                                // TODO: what is this?
                                type_: 1,
                                host: None,
                                lib: Some(lib.1),
                            });

                    let func_name = sframe.symbol
                        .as_ref()
                        .and_then(|s| s.symbol_offset.as_ref().map(|(name, _)| name.clone()))
                        .unwrap_or_else(|| format!("{:#x}", frame.addr));
                    let func = thread.funcs.get_or_insert_with(
                        (lib_path.clone(), func_name.clone()),
                        || Func {
                            name: thread.strings.get_or_insert_str(&func_name),
                            is_js: false,
                            relevant_for_js: false,
                            resource: resource.1 as isize,
                            file_name: None,
                            line_number: None,
                            column_number: None,
                        },
                    );

                    let native_symbol = sframe.symbol.as_ref().and_then(|s| {
                        s.symbol_offset.as_ref().map(|(name, offset)| {
                            thread
                                .native_symbols
                                .get_or_insert_with((lib_path.clone(), name.clone()), || {
                                    if *offset as u64 > frame.addr {
                                        error!(
                                            "symbol offset is greater than frame address: {:#x} > {:#x}",
                                            *offset, frame.addr
                                        );
                                        error!("symbol = {:?}", s);
                                        error!("frame = {:?}", frame);
                                        error!("sample = {:?}", sample);
                                    }
                                    NativeSymbol {
                                        lib_index: lib.1,
                                        address: (frame.addr - *offset as u64) - s.image_base,
                                        name: thread.strings.get_or_insert_str(name),
                                        // TODO
                                        function_size: None,
                                    }
                                })
                                .1
                        })
                    });

                    let address = frame.addr - sframe.symbol.as_ref().map(|s| s.image_base).unwrap_or(0);

                    FirefoxFrame {
                        address,
                        inline_depth: 0,
                        category: Some(self.categories.get(&frame.category).unwrap().1),
                        subcategory: Some(0),
                        func: func.1,
                        native_symbol,
                        inner_window_id: None,
                        implementation: None,
                        line: None,
                        column: None,
                    }
                });

                (ff_frame.0.category.unwrap(), ff_frame.1)
            })
            .collect::<Vec<_>>();

        let stack_index = thread.insert_stack(&stack_frames);

        thread.samples.push(FirefoxSample {
            stack: stack_index,
            time: Milliseconds((sample.time - self.info.start_time_abs).millis_f64()),
            weight: 1.0,
            thread_cpu_delta: Microseconds(sample.cpu_time_delta_us as f64),
        });

        Ok(())
    }

    pub fn add_ktrace_markers(&mut self, ktrace_results: &KtraceResults) {
        for (tid, kt_thread) in ktrace_results.threads.iter() {
            let Some(ff_thread) = self.threads.get_mut(tid) else {
                continue;
            };

            for (start, end) in kt_thread.faults.iter() {
                ff_thread.raw_markers.force_insert(
                    (),
                    RawMarker {
                        data: None,
                        name: ff_thread.strings.get_or_insert_str("MACH_vmfault"),
                        start_time: Some(Milliseconds(
                            (*start - self.info.start_time_abs).millis_f64(),
                        )),
                        end_time: Some(Milliseconds(
                            (*end - self.info.start_time_abs).millis_f64(),
                        )),
                        phase: MarkerPhase::INTERVAL,
                        category: self.categories.get(&FrameCategory::HostKernel).unwrap().1,
                        thread_id: Some(tid.0),
                    },
                );
            }
        }
    }

    pub fn add_resources<const N: usize>(&mut self, samples: &SegVec<ResourceSample, N>) {
        let mut mem_table = KeyedTable::<(), FirefoxCounterSample>::new();

        for sample in samples {
            let time = Milliseconds((sample.time - self.info.start_time_abs).millis_f64());

            mem_table.force_insert(
                (),
                FirefoxCounterSample {
                    time,
                    number: None,
                    count: sample.phys_footprint,
                },
            );
        }

        self.counters.push(FirefoxCounter {
            name: "phys_footprint".to_string(),
            category: "Memory".to_string(),
            description: "".to_string(),
            color: None,
            pid: self.info.pid.into(),
            main_thread_index: 0,
            samples: (&mem_table).into(),
        });
    }

    pub fn write_to_path(
        self,
        prof: &ProfileResults,
        total_bytes: usize,
        path: &str,
    ) -> anyhow::Result<()> {
        let file = File::create(path)?;

        // main thread will have the lowest mach port name
        let start_time = Milliseconds(
            self.info
                .start_time
                .duration_since(SystemTime::UNIX_EPOCH)?
                .as_nanos() as f64
                / 1_000_000.0,
        );
        let main_thread = self
            .threads
            .values()
            .min_by_key(|t| t.thread.id.0)
            .unwrap()
            .thread;

        let profile = FirefoxProfile {
            meta: ProfileMeta {
                categories: &self.categories.values,
                debug: cfg!(debug_assertions),
                extensions: ExtensionTable::default(),
                interval: Milliseconds(1000.0 / self.info.params.sample_rate as f64),
                preprocessed_profile_version: 49,
                process_type: 0,
                product: "OrbStack".to_string(),
                sample_units: SampleUnits {
                    event_delay: "ms".to_string(),
                    thread_cpu_delta: ThreadCPUDeltaUnit::Microseconds,
                    time: "ms".to_string(),
                },
                // Unix epoch time
                start_time,
                // our process never ended; the profile did
                end_time: None,
                // milliseconds relative to start
                profiling_start_time: Some(Milliseconds(0.0)),
                profiling_end_time: Some(Milliseconds(
                    (self.info.end_time.duration_since(self.info.start_time)?).as_nanos() as f64
                        / 1_000_000.0,
                )),
                symbolicated: true,
                version: 24,
                uses_only_one_stack_type: true,
                does_not_use_frame_implementation: true,
                source_code_is_not_on_searchfox: true,
                marker_schema: vec![],
                stackwalk: 1,
                imported_from: "OrbStack".to_string(),
                main_memory: Some(system_total_memory()),
                oscpu: Some(format!("macOS {}", sysctl_string("kern.osproductversion")?)),
                cpu_name: Some(sysctl_string("machdep.cpu.brand_string")?),
                logical_cpus: Some(available_parallelism()?.get()),
                configuration: Some(ProfilerConfiguration {
                    threads: vec![],
                    features: vec![],
                    capacity: total_bytes / 8, // 8-byte units
                    duration: self.info.params.duration_ms.map(|d| d / 1_000), // ms -> seconds
                }),
                app_build_id: self
                    .info
                    .params
                    .app_commit
                    .as_ref()
                    .map(|c| c[..12].to_string()),
                source_url: self
                    .info
                    .params
                    .app_commit
                    .as_ref()
                    .map(|c| format!("https://github.com/orbstack/macvirt/commit/{}", c)),
                misc: self.info.params.app_version.clone(),
            },
            libs: &self.libs.values,
            threads: self
                .threads
                .values()
                .map(|t| FirefoxThread {
                    tid: t.thread.id.0,
                    name: t.thread.display_name(),
                    // TODO: ?
                    process_type: "default".to_string(),
                    resource_table: (&t.resources).into(),
                    samples: t.samples.clone(),
                    string_array: &t.strings.values,
                    markers: (&t.raw_markers).into(),
                    native_symbols: (&t.native_symbols).into(),
                    func_table: (&t.funcs).into(),
                    frame_table: (&t.frames).into(),
                    stack_table: (&t.stacks).into(),
                    process_name: image_basename(
                        std::env::current_exe().unwrap().to_str().unwrap(),
                    )
                    .to_string(),
                    is_main_thread: t.thread.id == main_thread.id,
                    pid: self.info.pid.into(),
                    paused_ranges: vec![],
                    process_startup_time: Milliseconds(0.0),
                    process_shutdown_time: None,
                    // threads can be added before sample collection starts
                    register_time: Milliseconds(
                        (t.thread.added_at.saturating_sub(self.info.start_time_abs)).millis_f64(),
                    ),
                    unregister_time: t
                        .thread
                        .stopped_at
                        .map(|t| Milliseconds((t - self.info.start_time_abs).millis_f64())),
                    show_markers_in_timeline: Some(true),
                })
                .collect(),
            counters: self.counters,
            profiler_overhead: vec![ProfilerOverhead {
                // not used by profiler UI
                samples: ProfilerOverheadSampleTable::default(),

                // Firefox Profiler doesn't let us set names for these, so:
                // Overhead = total sampler loop time to sample a batch of all threads
                // Cleaning = time for vCPU to sample guest stack
                // Interval = total time a vCPU was unable to run guest code. (time between thread_suspend completion, and vCPU finishing sampling. includes resume and scheduler/runnable overhead)
                // Lockings = time to sample host stack while thread is suspended. (time between thread_suspended and thread_resume)
                statistics: Some(ProfilerOverheadStats {
                    min_overhead: Microseconds((prof.sample_batch_histogram.min() as f64) / 1000.0),
                    max_overhead: Microseconds((prof.sample_batch_histogram.max() as f64) / 1000.0),
                    mean_overhead: Microseconds(prof.sample_batch_histogram.mean() / 1000.0),

                    min_lockings: Microseconds(
                        (prof.thread_suspend_histogram.min() as f64) / 1000.0,
                    ),
                    max_lockings: Microseconds(
                        (prof.thread_suspend_histogram.max() as f64) / 1000.0,
                    ),
                    mean_lockings: Microseconds(prof.thread_suspend_histogram.mean() / 1000.0),

                    min_cleaning: Microseconds(
                        (prof.vcpu_agg_histograms.sample_time.min() as f64) / 1000.0,
                    ),
                    max_cleaning: Microseconds(
                        (prof.vcpu_agg_histograms.sample_time.max() as f64) / 1000.0,
                    ),
                    mean_cleaning: Microseconds(
                        prof.vcpu_agg_histograms.sample_time.mean() / 1000.0,
                    ),

                    min_interval: Microseconds(
                        (prof.vcpu_agg_histograms.resume_and_sample.min() as f64) / 1000.0,
                    ),
                    max_interval: Microseconds(
                        (prof.vcpu_agg_histograms.resume_and_sample.max() as f64) / 1000.0,
                    ),
                    mean_interval: Microseconds(
                        prof.vcpu_agg_histograms.resume_and_sample.mean() / 1000.0,
                    ),

                    sampling_count: self.info.num_samples,
                    ..Default::default()
                }),
                pid: self.info.pid.into(),
                main_thread_index: 0,
            }],
        };

        let mut buf_writer = BufWriter::new(file);
        serde_json::to_writer(&mut buf_writer, &profile)?;
        Ok(())
    }
}
