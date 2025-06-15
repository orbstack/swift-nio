use bytemuck::{Pod, Zeroable};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Pod, Zeroable)]
#[repr(transparent)]
pub struct BeU16(u16);

#[derive(Debug, Clone, Copy, PartialEq, Eq, Pod, Zeroable)]
#[repr(transparent)]
pub struct BeU32(u32);

#[derive(Debug, Clone, Copy, PartialEq, Eq, Pod, Zeroable)]
#[repr(transparent)]
pub struct BeU64(u64);

impl BeU16 {
    pub fn new(value: u16) -> Self {
        Self(value.to_be())
    }

    pub fn get(self) -> u16 {
        u16::from_be(self.0)
    }
}

impl BeU32 {
    pub fn new(value: u32) -> Self {
        Self(value.to_be())
    }

    pub fn get(self) -> u32 {
        u32::from_be(self.0)
    }
}

impl BeU64 {
    pub fn new(value: u64) -> Self {
        Self(value.to_be())
    }

    pub fn get(self) -> u64 {
        u64::from_be(self.0)
    }
}

impl From<u16> for BeU16 {
    fn from(value: u16) -> Self {
        Self::new(value)
    }
}

impl From<BeU16> for u16 {
    fn from(value: BeU16) -> Self {
        value.get()
    }
}

impl From<u32> for BeU32 {
    fn from(value: u32) -> Self {
        Self::new(value)
    }
}

impl From<BeU32> for u32 {
    fn from(value: BeU32) -> Self {
        value.get()
    }
}

impl From<u64> for BeU64 {
    fn from(value: u64) -> Self {
        Self::new(value)
    }
}

impl From<BeU64> for u64 {
    fn from(value: BeU64) -> Self {
        value.get()
    }
}