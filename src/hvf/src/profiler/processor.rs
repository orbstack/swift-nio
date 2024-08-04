use std::fmt::Display;
use std::io::{BufWriter, Write};
use std::{cell::RefCell, collections::BTreeMap, fs::File, rc::Rc};

use ahash::AHashMap;

use super::{
    symbolicator::SymbolResult,
    thread::{ProfileeThread, ThreadId},
    Sample,
};
use super::{Frame, ProfileInfo, SampleCategory, SymbolicatedFrame};

trait AsTreeKey {
    type Key: Ord;

    fn as_tree_key(&self) -> Self::Key;
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

    pub fn insert(&mut self, stack_iter: &mut impl Iterator<Item = SampleNode>) {
        self.count += 1;

        if let Some(data) = stack_iter.next() {
            let mut child = self
                .children
                .entry(data.as_tree_key())
                .or_insert_with(|| Rc::new(RefCell::new(StackTree::new())))
                .borrow_mut();
            child.data = Some(data.clone());
            child.insert(stack_iter);
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

fn image_basename(image: &str) -> &str {
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
            Some(s) => match &s.symbol_offset {
                Some((sym, offset)) => {
                    write!(f, "{}+{}  ({})", sym, offset, image_basename(&s.image))
                }
                None => write!(f, "{:#x}  ({})", self.frame.addr, image_basename(&s.image)),
            },
            None => write!(f, "{:#x}", self.frame.addr),
        }
    }
}

// it's more accurate to key by exact address, but the output is uglier
#[derive(Debug, Clone, Ord, PartialOrd, Eq, PartialEq)]
enum SymbolTreeKey {
    Symbol(SampleCategory, String),
    Addr(SampleCategory, u64),
}

impl AsTreeKey for SampleNode {
    type Key = SymbolTreeKey;

    fn as_tree_key(&self) -> SymbolTreeKey {
        match &self.symbol {
            Some(s) => match &s.symbol_offset {
                Some((sym, _)) => SymbolTreeKey::Symbol(self.frame.category, sym.clone()),
                None => SymbolTreeKey::Addr(self.frame.category, self.frame.addr),
            },
            None => SymbolTreeKey::Addr(self.frame.category, self.frame.addr),
        }
    }
}

struct ThreadNode {
    name: Option<String>,
    stacks: StackTree<SampleNode>,
}

pub struct TextSampleProcessor<'a> {
    threads_map: AHashMap<ThreadId, &'a ProfileeThread>,

    threads: BTreeMap<ThreadId, ThreadNode>,
}

impl<'a> TextSampleProcessor<'a> {
    pub fn new(
        _info: &'a ProfileInfo,
        threads_map: AHashMap<ThreadId, &'a ProfileeThread>,
    ) -> anyhow::Result<Self> {
        Ok(Self {
            threads_map,

            threads: BTreeMap::new(),
        })
    }

    pub fn process_sample(
        &mut self,
        sample: &Sample,
        frames: &[SymbolicatedFrame],
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
            .insert(&mut frames.iter().rev().map(|frame| SampleNode {
                frame: frame.frame,
                symbol: frame.symbol.clone(),
            }));

        Ok(())
    }

    pub fn write_to_path(&self, path: &str) -> anyhow::Result<()> {
        let file = File::create(path)?;
        let mut buf_writer = BufWriter::new(file);

        // sorted by ID
        let threads = self.threads.iter().collect::<Vec<_>>();
        for (thread_id, thread_node) in threads {
            writeln!(
                buf_writer,
                "\n\nThread '{}'  ({:#x}, {} samples)",
                thread_node.name.as_deref().unwrap_or(""),
                thread_id.0,
                thread_node.stacks.count
            )?;

            thread_node.stacks.dump(&mut buf_writer, 1)?;
        }

        Ok(())
    }
}
