use std::fmt::Display;
use std::io::Write;
use std::{
    cell::RefCell,
    collections::{BTreeMap, HashMap},
    fs::File,
    rc::Rc,
};
use tracing::error;

use super::symbolicator::{CachedSymbolicator, LinuxSymbolicator};
use super::SampleCategory;
use super::{
    symbolicator::{MacSymbolicator, SymbolResult, Symbolicator},
    thread::{ProfileeThread, ThreadId},
    Sample,
};

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
                data.category.as_char(),
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
    category: SampleCategory,
    addr: u64,
    symbol: Option<SymbolResult>,
}

impl Display for SampleNode {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match &self.symbol {
            Some(s) => match &s.symbol_offset {
                Some((sym, offset)) => {
                    write!(f, "{}+{}  ({})", sym, offset, image_basename(&s.image))
                }
                None => write!(f, "{:#x}  ({})", self.addr, image_basename(&s.image)),
            },
            None => write!(f, "{:#x}", self.addr),
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
                Some((sym, _)) => SymbolTreeKey::Symbol(self.category, sym.clone()),
                None => SymbolTreeKey::Addr(self.category, self.addr),
            },
            None => SymbolTreeKey::Addr(self.category, self.addr),
        }
    }
}

struct ThreadNode {
    name: Option<String>,
    stacks: StackTree<SampleNode>,
}

pub struct SampleProcessor<'a> {
    threads_map: HashMap<ThreadId, &'a ProfileeThread>,

    threads: BTreeMap<ThreadId, ThreadNode>,
    host_symbolicator: CachedSymbolicator<MacSymbolicator>,
    guest_symbolicator: Option<&'a LinuxSymbolicator>,
}

impl<'a> SampleProcessor<'a> {
    pub fn new(
        threads_map: HashMap<ThreadId, &'a ProfileeThread>,
        guest_symbolicator: Option<&'a LinuxSymbolicator>,
    ) -> anyhow::Result<Self> {
        Ok(Self {
            threads_map,

            threads: BTreeMap::new(),
            host_symbolicator: CachedSymbolicator::new(MacSymbolicator {}),
            guest_symbolicator,
        })
    }

    pub fn process_sample(&mut self, sample: &Sample) -> anyhow::Result<()> {
        // process sample
        let thread_node = self
            .threads
            .entry(sample.thread_id)
            .or_insert_with(|| ThreadNode {
                name: self
                    .threads_map
                    .get(&sample.thread_id)
                    .map(|t| t.name.clone()),
                stacks: StackTree::new(),
            });

        thread_node
            .stacks
            .insert(&mut sample.stack.iter().rev().map(|&(category, addr)| {
                let sym_result = match category {
                    SampleCategory::HostUserspace => self.host_symbolicator.addr_to_symbol(addr),
                    SampleCategory::GuestKernel => match self.guest_symbolicator {
                        Some(s) => s.addr_to_symbol(addr),
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
                        error!("failed to symbolicate addr {:x}: {}", addr, e);
                        None
                    }
                };

                SampleNode {
                    category,
                    addr,
                    symbol: sym,
                }
            }));

        Ok(())
    }

    pub fn write_to_path(&self, path: &str) -> anyhow::Result<()> {
        let mut file = File::create(path)?;

        // sort by name, not by ID
        let mut threads = self.threads.iter().collect::<Vec<_>>();
        threads.sort_by_key(|(_, t)| t.name.clone());
        for (thread_id, thread_node) in threads {
            writeln!(
                file,
                "\n\nThread '{}'  ({:#x}, {} samples)",
                thread_node.name.as_deref().unwrap_or("unknown"),
                thread_id.0,
                thread_node.stacks.count
            )?;

            thread_node.stacks.dump(&mut file, 1)?;
        }

        Ok(())
    }
}
