use std::fmt::Write;
use smallvec::SmallVec;

#[derive(Default)]
pub struct SparseFileMap {
    // SmallVec with 1 element in case we were wrong (e.g. due to compression) and this file isn't actually sparse
    entries: SmallVec<[SparseFileMapEntry; 1]>,
}

impl SparseFileMap {
    pub fn add(&mut self, offset: u64, len: u64) {
        self.entries.push(SparseFileMapEntry { offset, len });
    }

    // a file is not actually sparse if:
    // - only 1 entry in map
    // - offset = 0
    // - len = st_size
    pub fn is_contiguous(&self, expected_size: u64) -> bool {
        self.entries.len() == 1 && self.entries[0].offset == 0 && self.entries[0].len == expected_size
    }

    pub fn serialize(&self) -> String {
        let mut buf = format!("{}\n", self.entries.len());
        for entry in self.entries.iter() {
            write!(buf, "{}\n{}\n", entry.offset, entry.len).unwrap();
        }
        buf
    }

    pub fn payload_bytes(&self) -> u64 {
        self.entries.iter().map(|e| e.len).sum()
    }

    pub fn iter_entries(&self) -> impl Iterator<Item = &SparseFileMapEntry> {
        self.entries.iter()
    }
}

pub struct SparseFileMapEntry {
    pub offset: u64,
    pub len: u64,
}
