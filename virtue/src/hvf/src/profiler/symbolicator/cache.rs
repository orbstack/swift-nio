use ahash::AHashMap;

use super::{SymbolResults, Symbolicator};

pub struct CachedSymbolicator<S> {
    cache: AHashMap<u64, SymbolResults>,
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
    fn addr_to_symbols(&mut self, addr: u64) -> anyhow::Result<SymbolResults> {
        if let Some(symbols) = self.cache.get(&addr) {
            return Ok(symbols.clone());
        }

        let symbols = self.inner.addr_to_symbols(addr)?;
        self.cache.insert(addr, symbols.clone());
        Ok(symbols)
    }
}
