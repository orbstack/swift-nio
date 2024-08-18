use std::collections::VecDeque;
use std::fmt::Display;
use std::io::{BufWriter, Write};
use std::{cell::RefCell, collections::BTreeMap, fs::File, rc::Rc};

use ahash::AHashMap;
use string_interner::symbol::SymbolU32;
use string_interner::{DefaultBackend, StringInterner};
use time::format_description::well_known::Rfc2822;
use time::OffsetDateTime;

use crate::profiler::stats::HistogramExt;
use crate::profiler::symbolicator::{SymbolFunc, SymbolResult};
use crate::profiler::{
    thread::{ProfileeThread, ThreadId},
    Sample,
};
use crate::profiler::{Frame, FrameCategory, ProfileInfo, ProfileResults, SymbolicatedFrame};

trait AsTreeKey {
    type Key: Ord;

    fn as_tree_key(&self, interner: &mut StringInterner<DefaultBackend>) -> Self::Key;
}

struct StackTree<D: Display + Clone + AsTreeKey<Key = K>, K = <D as AsTreeKey>::Key> {
    children: BTreeMap<K, Rc<RefCell<StackTree<D, K>>>>,

    data: Option<D>,
    count: u64,
}

impl StackTree<SampleNode, <SampleNode as AsTreeKey>::Key> {
    pub fn new() -> Self {
        Self {
            children: BTreeMap::new(),
            data: None,
            count: 0,
        }
    }

    pub fn insert<'a>(
        &mut self,
        interner: &mut StringInterner<DefaultBackend>,
        stack_iter: &mut impl Iterator<Item = SampleNodeRef<'a>>,
    ) {
        self.count += 1;

        if let Some(data) = stack_iter.next() {
            let mut child = self
                .children
                .entry(data.as_tree_key(interner))
                .or_insert_with(|| Rc::new(RefCell::new(StackTree::new())))
                .borrow_mut();
            child.data = Some(SampleNode {
                frame: data.frame,
                symbol: data.symbol.cloned(),
            });
            child.insert(interner, stack_iter);
        }
    }

    pub fn dump(&self, w: &mut impl Write, indent: usize) -> anyhow::Result<()> {
        // sort by count (ascending), not by symbol key
        let mut children = self.children.iter().collect::<Vec<_>>();
        children.sort_by_key(|(_, c)| c.borrow().count);

        for (_, child) in children.iter().rev() {
            let child = child.borrow();
            let indent_str = " ".repeat(indent * 2);
            let data = match &child.data {
                Some(d) => d,
                None => continue,
            };
            writeln!(
                w,
                "{}{} {:<5}   {}",
                indent_str,
                data.frame.category.as_char(),
                child.count,
                data
            )?;
            child.dump(w, indent + 1)?;
        }

        Ok(())
    }
}

fn basename(image: &str) -> &str {
    image.rsplit('/').next().unwrap_or(image)
}

#[derive(Debug, Clone)]
struct SampleNode {
    frame: Frame,
    symbol: Option<SymbolResult>,
}

impl Display for SampleNode {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match &self.symbol {
            Some(s) => match &s.function {
                Some(SymbolFunc::Function(sym, offset)) => {
                    write!(f, "{}+{}  ({})", sym, offset, basename(&s.image))
                }
                Some(SymbolFunc::Inlined(sym)) => {
                    write!(f, "[inl] {}  ({})", sym, basename(&s.image))
                }
                None => write!(f, "{:#x}  ({})", self.frame.addr, basename(&s.image)),
            },
            None => write!(f, "{:#x}", self.frame.addr),
        }
    }
}

// it's more accurate to key by exact address, but the output is uglier
#[derive(Debug, Clone, Ord, PartialOrd, Eq, PartialEq)]
enum SymbolTreeKey {
    Symbol(FrameCategory, SymbolU32),
    Addr(FrameCategory, u64),
}

struct SampleNodeRef<'a> {
    frame: Frame,
    symbol: Option<&'a SymbolResult>,
}

impl<'a> AsTreeKey for SampleNodeRef<'a> {
    type Key = SymbolTreeKey;

    fn as_tree_key(&self, interner: &mut StringInterner<DefaultBackend>) -> SymbolTreeKey {
        match &self.symbol {
            Some(s) => match &s.function {
                Some(func) => {
                    SymbolTreeKey::Symbol(self.frame.category, interner.get_or_intern(func.name()))
                }
                None => SymbolTreeKey::Addr(self.frame.category, self.frame.addr),
            },
            None => SymbolTreeKey::Addr(self.frame.category, self.frame.addr),
        }
    }
}

impl AsTreeKey for SampleNode {
    type Key = SymbolTreeKey;

    fn as_tree_key(&self, interner: &mut StringInterner<DefaultBackend>) -> SymbolTreeKey {
        SampleNodeRef {
            frame: self.frame,
            symbol: self.symbol.as_ref(),
        }
        .as_tree_key(interner)
    }
}

struct ThreadNode {
    name: Option<String>,
    stacks: StackTree<SampleNode>,
}

pub struct TextExporter<'a> {
    info: &'a ProfileInfo,
    threads_map: &'a AHashMap<ThreadId, &'a ProfileeThread>,

    threads: BTreeMap<ThreadId, ThreadNode>,
    interner: StringInterner<DefaultBackend>,
}

impl<'a> TextExporter<'a> {
    pub fn new(
        info: &'a ProfileInfo,
        threads_map: &'a AHashMap<ThreadId, &'a ProfileeThread>,
    ) -> anyhow::Result<Self> {
        Ok(Self {
            info,
            threads_map,

            threads: BTreeMap::new(),
            interner: StringInterner::new(),
        })
    }

    pub fn process_sample(
        &mut self,
        sample: &Sample,
        frames: &VecDeque<SymbolicatedFrame>,
    ) -> anyhow::Result<()> {
        // process sample
        let thread_node = self
            .threads
            .entry(sample.thread_id)
            .or_insert_with(|| ThreadNode {
                name: self
                    .threads_map
                    .get(&sample.thread_id)
                    .and_then(|t| t.name.clone()),
                stacks: StackTree::new(),
            });

        thread_node
            .stacks
            // top -> bottom
            .insert(
                &mut self.interner,
                &mut frames.iter().rev().flat_map(|sframe| {
                    sframe.iter_symbols().map(|s| SampleNodeRef {
                        frame: sframe.frame,
                        symbol: s,
                    })
                }),
            );

        Ok(())
    }

    pub fn write_to_path(&self, prof: &ProfileResults, path: &str) -> anyhow::Result<()> {
        let file = File::create(path)?;
        let mut w = BufWriter::new(file);

        // write basic info
        writeln!(
            w,
            "App version: {}",
            self.info.params.app_version.as_deref().unwrap_or(""),
        )?;
        writeln!(
            w,
            "App build number: {}",
            self.info.params.app_build_number.unwrap_or(0),
        )?;
        writeln!(
            w,
            "App commit: {}",
            self.info.params.app_commit.as_deref().unwrap_or(""),
        )?;
        writeln!(w, "Executable: {}", std::env::current_exe()?.display(),)?;
        writeln!(w)?;

        writeln!(w, "PID: {}", self.info.pid)?;
        let start_time: OffsetDateTime = self.info.start_time.into();
        writeln!(w, "Started at: {}", start_time.format(&Rfc2822)?)?;
        writeln!(
            w,
            "Duration: {:?}",
            self.info.end_time_abs - self.info.start_time_abs
        )?;
        writeln!(w, "Samples: {}", self.info.num_samples)?;
        writeln!(w, "Sample rate: {} Hz", self.info.params.sample_rate)?;

        // sorted by ID
        let threads = self.threads.iter().collect::<Vec<_>>();
        for (thread_id, thread_node) in threads {
            writeln!(
                w,
                "\n\nThread '{}'  ({:#x}, {} samples)",
                thread_node.name.as_deref().unwrap_or(""),
                thread_id.0,
                thread_node.stacks.count
            )?;

            thread_node.stacks.dump(&mut w, 1)?;
        }

        // histograms
        writeln!(w, "\n\n\nProfiler overhead:")?;
        prof.sample_batch_histogram
            .dump(&mut w, "Sampler loop iteration — all threads (ns)")?;
        prof.thread_suspend_histogram
            .dump(&mut w, "Thread suspend + host stack sampling (ns)")?;
        prof.vcpu_agg_histograms
            .sample_time
            .dump(&mut w, "vCPU sampling (ns)")?;
        prof.vcpu_agg_histograms.resume_and_sample.dump(
            &mut w,
            "vCPU total overhead — suspended + host sampling + resume + guest sampling (ns)",
        )?;

        Ok(())
    }
}
