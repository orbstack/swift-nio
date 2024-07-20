use bincode::{Decode, Encode};

#[derive(Encode, Decode, PartialEq, Debug)]
struct CompactSystemMapSymbol {
    vaddr: u64,
    name: String,
}

#[derive(Encode, Decode, PartialEq, Debug)]
pub struct CompactSystemMap {
    symbols: Vec<CompactSystemMapSymbol>,
}

impl CompactSystemMap {
    pub fn from_slice(data: &[u8]) -> anyhow::Result<Self> {
        let config = bincode::config::standard()
            .with_little_endian()
            .with_variable_int_encoding();
        let (csmap, _) = bincode::decode_from_slice(data, config)?;
        Ok(csmap)
    }

    pub fn vaddr_to_symbol(&self, vaddr: u64) -> Option<(&str, usize)> {
        // pre-sorted, so we can use binary search
        let partition_idx = self.symbols.partition_point(|s| s.vaddr <= vaddr);

        // last candidate is the one we want
        let candidates = &self.symbols[..partition_idx];
        candidates
            .last()
            .map(|s| (s.name.as_str(), (vaddr - s.vaddr) as usize))
    }

    pub fn symbol_to_vaddr(&self, symbol: &str) -> Option<u64> {
        self.symbols
            .iter()
            .find(|s| s.name == symbol)
            .map(|s| s.vaddr)
    }
}
