use anyhow::anyhow;
use serde::{Deserialize, Serialize};
use std::hash::Hash;
use std::ops::Sub;
use std::time::SystemTime;
use std::{collections::HashMap, fs::File};
use tracing::error;

use super::symbolicator::{CachedSymbolicator, LinuxSymbolicator};
use super::{
    symbolicator::{MacSymbolicator, SymbolResult, Symbolicator},
    thread::{ProfileeThread, ThreadId},
    Sample,
};
use super::{Frame, ProfileInfo, SampleCategory};

#[derive(Serialize, Debug, Clone, PartialEq, Eq, Hash)]
#[serde(transparent)]
struct Pid(i32);

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct FirefoxProfile<'a> {
    meta: ProfileMeta<'a>,
    libs: &'a [Lib],
    counters: Vec<TODO>,
    profiler_overhead: Vec<ProfilerOverhead>,
    threads: Vec<FirefoxThread<'a>>,
}

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct ProfileMeta<'a> {
    interval: Milliseconds,
    start_time: Milliseconds,
    end_time: Milliseconds,
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

    symbolicated: bool,
    uses_only_one_stack_type: bool,
    does_not_use_frame_implementation: bool,
    source_code_is_not_on_searchfox: bool,

    marker_schema: Vec<TODO>,
}

macro_rules! table_type {
    ($base_type:ident$(<$base_lt:lifetime>)? {$(
        $(#[$attr:meta])*
        $vis:vis $name:ident : $ty:ty,
    )*}, $table_type:ident$(<$table_lt:lifetime>)?$( {
        $($extra_vis:vis $extra_name:ident : $extra_ty:ty = $extra_default:expr,)*
    })?) => {
        struct $base_type$(<$base_lt>)? {
            $(
                $vis $name: $ty,
            )*
        }

        #[derive(Serialize, Debug, Clone)]
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
            fn push(&mut self, value: $base_type$(<$table_lt>)?) {
                self.length += 1;
                $(
                    self.$name.push(value.$name);
                )*
            }
        }

        impl<$($table_lt,)? _KeyType> From<&KeyedTable<_KeyType, $base_type$(<$table_lt>)?>> for $table_type$(<$table_lt>)? {
            fn from(keyed: &KeyedTable<_KeyType, $base_type$(<$table_lt>)?>) -> Self {
                let mut table = Self {
                    length: keyed.values.len(),
                    $(
                        $name: Vec::with_capacity(keyed.values.len()),
                    )*
                    $(
                        $($extra_name: $extra_default,)*
                    )?
                };

                for value in &keyed.values {
                    $(
                        table.$name.push(value.$name.clone());
                    )*
                }

                table
            }
        }

        impl$(<$table_lt>)? Default for $table_type$(<$table_lt>)? {
            fn default() -> Self {
                Self {
                    length: 0,
                    $(
                        $name: Vec::new(),
                    )*
                    $(
                        $($extra_name: $extra_default,)*
                    )?
                }
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

#[derive(Serialize, Debug, Clone)]
#[serde(transparent)]
struct Milliseconds(f64);

impl Sub for Milliseconds {
    type Output = Milliseconds;

    fn sub(self, rhs: Self) -> Self::Output {
        Milliseconds(self.0 - rhs.0)
    }
}

#[derive(Serialize, Debug, Clone)]
#[serde(transparent)]
struct Microseconds(u64);

table_type!(
    CounterSample {
        time: Milliseconds,
        // number of times 'count' was changed since previous sample
        number: u64,
        // value
        count: u64,
    },
    CounterSampleTable
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

#[derive(Serialize, Debug, Clone)]
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
    sampling_count: Microseconds,
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

type TODO = ();

#[derive(Serialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
struct FirefoxThread<'a> {
    process_type: String,
    process_startup_time: Milliseconds,
    process_shutdown_time: Option<Milliseconds>,
    register_time: Milliseconds,
    unregister_time: Option<Milliseconds>,
    paused_ranges: Vec<TODO>,
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

#[derive(Serialize, Debug, Clone)]
enum WeightType {
    #[serde(rename = "samples")]
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
    weight_type: WeightType = WeightType::Samples,
});

table_type!(RawMarker {
    data: Option<TODO>,
    name: IndexIntoStringTable,
    start_time: Option<Milliseconds>,
    end_time: Option<Milliseconds>,
    phase: TODO,
    category: IndexIntoCategoryList,
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
            values: vec![],
            keys: HashMap::new(),
        }
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
            self.keys.insert(key, self.values.len());
            self.values.push(value);
            (&self.values[self.values.len() - 1], self.values.len() - 1)
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
}

impl ThreadState<'_> {
    fn insert_stack(
        &mut self,
        stack: &[(IndexIntoCategoryList, IndexIntoFrameTable)],
    ) -> IndexIntoStackTable {
        let stack_vec = stack.to_vec();
        if let Some((_, index)) = self.stacks.get(&stack_vec) {
            return index;
        }

        let (category_i, frame_i) = stack[0];
        let prefix_frames = &stack[1..];
        let ff_stack = FirefoxStack {
            frame: frame_i,
            category: category_i,
            subcategory: 0,
            prefix: if prefix_frames.is_empty() {
                None
            } else {
                Some(self.insert_stack(prefix_frames))
            },
        };
        self.stacks.get_or_insert(stack_vec, ff_stack).1
    }
}

fn image_basename(image: &str) -> &str {
    image.rsplit('/').next().unwrap_or(image)
}

pub struct FirefoxSampleProcessor<'a> {
    info: &'a ProfileInfo,
    threads: HashMap<ThreadId, ThreadState<'a>>,

    categories: KeyedTable<SampleCategory, FirefoxCategory>,
    libs: KeyedTable<String, Lib>,

    host_symbolicator: CachedSymbolicator<MacSymbolicator>,
    guest_symbolicator: Option<&'a LinuxSymbolicator>,
}

impl<'a> FirefoxSampleProcessor<'a> {
    pub fn new(
        info: &'a ProfileInfo,
        threads_map: HashMap<ThreadId, &'a ProfileeThread>,
        guest_symbolicator: Option<&'a LinuxSymbolicator>,
    ) -> anyhow::Result<Self> {
        let mut categories = KeyedTable::new();
        categories.get_or_insert(
            SampleCategory::GuestUserspace,
            FirefoxCategory {
                name: "Guest Userspace".to_string(),
                color: "grey".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );
        categories.get_or_insert(
            SampleCategory::GuestKernel,
            FirefoxCategory {
                name: "Guest Kernel".to_string(),
                color: "green".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );
        categories.get_or_insert(
            SampleCategory::HostUserspace,
            FirefoxCategory {
                name: "Host Userspace".to_string(),
                color: "yellow".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );
        categories.get_or_insert(
            SampleCategory::HostKernel,
            FirefoxCategory {
                name: "Host Kernel".to_string(),
                color: "blue".to_string(),
                subcategories: vec!["Other".to_string()],
            },
        );

        Ok(Self {
            info,
            threads: threads_map
                .into_iter()
                .map(|(id, thread)| {
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
                        },
                    )
                })
                .collect(),

            categories,
            libs: KeyedTable::new(),

            host_symbolicator: CachedSymbolicator::new(MacSymbolicator {}),
            guest_symbolicator,
        })
    }

    pub fn process_sample(&mut self, sample: &Sample) -> anyhow::Result<()> {
        let thread = self
            .threads
            .get_mut(&sample.thread_id)
            .ok_or_else(|| anyhow!("thread not found: {:?}", sample.thread_id))?;

        let stack_frames = sample
            .stack
            .iter()
            .map(|&frame| {
                // find frame for this (category, addr)
                let ff_frame = thread.frames.get_or_insert_with(frame, || {
                    let sym_result = match frame.category {
                        SampleCategory::HostUserspace => {
                            self.host_symbolicator.addr_to_symbol(frame.addr)
                        }
                        SampleCategory::GuestKernel => match self.guest_symbolicator {
                            Some(s) => s.addr_to_symbol(frame.addr),
                            None => Ok(None),
                        },
                        SampleCategory::GuestUserspace => Ok(Some(SymbolResult {
                            image: "guest".to_string(),
                            image_base: 0,
                            symbol_offset: Some(("<GUEST USERSPACE>".to_string(), 0)),
                        })),
                        _ => Ok(None),
                    };
                    let sym = match sym_result {
                        Ok(r) => r,
                        Err(e) => {
                            error!("failed to symbolicate addr {:x}: {}", frame.addr, e);
                            None
                        }
                    };

                    let lib_path = sym
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

                    let func_name = sym
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

                    let native_symbol = sym.as_ref().and_then(|s| {
                        s.symbol_offset.as_ref().map(|(name, offset)| {
                            thread
                                .native_symbols
                                .get_or_insert_with((lib_path.clone(), name.clone()), || {
                                    NativeSymbol {
                                        lib_index: lib.1,
                                        address: frame.addr - *offset as u64,
                                        name: thread.strings.get_or_insert_str(name),
                                        // TODO
                                        function_size: None,
                                    }
                                })
                                .1
                        })
                    });

                    let address = frame.addr - sym.map(|s| s.image_base).unwrap_or(0);

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
            stack: Some(stack_index),
            time: Milliseconds((sample.timestamp - self.info.start_time_abs).millis_f64()),
            weight: 1.0,
            thread_cpu_delta: Microseconds(sample.cpu_time_delta_us),
        });

        Ok(())
    }

    pub fn write_to_path(&self, path: &str) -> anyhow::Result<()> {
        let mut file = File::create(path)?;

        // main thread will have the lowest mach port name
        let start_time = Milliseconds(
            self.info
                .start_time
                .duration_since(SystemTime::UNIX_EPOCH)?
                .as_nanos() as f64
                / 1_000_000.0,
        );
        let end_time = Milliseconds(
            self.info
                .end_time
                .duration_since(SystemTime::UNIX_EPOCH)?
                .as_nanos() as f64
                / 1_000_000.0,
        );
        let main_thread_tid = self
            .threads
            .values()
            .min_by_key(|t| t.thread.id().0)
            .map(|t| t.thread.id().0)
            .unwrap_or(0);

        let profile = FirefoxProfile {
            meta: ProfileMeta {
                categories: &self.categories.values,
                debug: cfg!(debug_assertions),
                extensions: ExtensionTable::default(),
                interval: Milliseconds(1_000.0 / self.info.params.sample_rate as f64),
                preprocessed_profile_version: 46,
                process_type: 0,
                product: "OrbStack".to_string(),
                sample_units: SampleUnits {
                    event_delay: "ms".to_string(),
                    thread_cpu_delta: ThreadCPUDeltaUnit::Microseconds,
                    time: "ms".to_string(),
                },
                start_time,
                end_time,
                profiling_start_time: None,
                profiling_end_time: None,
                symbolicated: true,
                version: 24,
                uses_only_one_stack_type: true,
                does_not_use_frame_implementation: true,
                source_code_is_not_on_searchfox: true,
                marker_schema: vec![],
                stackwalk: 1,
                imported_from: "OrbStack".to_string(),
            },
            libs: &self.libs.values,
            threads: self
                .threads
                .values()
                .map(|t| FirefoxThread {
                    tid: t.thread.id().0 as u64,
                    name: t.thread.name.clone(),
                    // TODO: ?
                    process_type: "default".to_string(),
                    resource_table: (&t.resources).into(),
                    samples: t.samples.clone(),
                    string_array: &t.strings.values,
                    markers: RawMarkerTable::default(),
                    native_symbols: (&t.native_symbols).into(),
                    func_table: (&t.funcs).into(),
                    frame_table: (&t.frames).into(),
                    stack_table: (&t.stacks).into(),
                    process_name: image_basename(
                        std::env::current_exe().unwrap().to_str().unwrap(),
                    )
                    .to_string(),
                    is_main_thread: t.thread.id().0 == main_thread_tid,
                    pid: Pid(self.info.pid),
                    paused_ranges: vec![],
                    process_startup_time: Milliseconds(0.0),
                    process_shutdown_time: None,
                    register_time: Milliseconds(
                        (t.thread.added_at - self.info.start_time_abs).millis_f64(),
                    ),
                    unregister_time: t
                        .thread
                        .stopped_at
                        .map(|t| Milliseconds((t - self.info.start_time_abs).millis_f64())),
                    show_markers_in_timeline: Some(true),
                })
                .collect(),
            counters: vec![],
            profiler_overhead: vec![],
        };

        serde_json::to_writer(&file, &profile)?;
        Ok(())
    }
}
