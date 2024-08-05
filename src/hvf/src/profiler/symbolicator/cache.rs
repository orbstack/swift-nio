use ahash::AHashMap;

use super::{SymbolResult, Symbolicator};

pub struct CachedSymbolicator<S> {
    cache: AHashMap<u64, Option<SymbolResult>>,
    inner: S,
}

impl<S: Symbolicator> CachedSymbolicator<S> {
    pub fn new(inner: S) -> Self {
        Self {
            cache: AHashMap::new(),
            inner,
        }
    }
}

impl<S: Symbolicator> Symbolicator for CachedSymbolicator<S> {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        if let Some(symbol) = self.cache.get(&addr) {
            return Ok(symbol.clone());
        }

        let symbol = self.inner.addr_to_symbol(addr)?;
        self.cache.insert(addr, symbol.clone());
        Ok(symbol)
    }
}
