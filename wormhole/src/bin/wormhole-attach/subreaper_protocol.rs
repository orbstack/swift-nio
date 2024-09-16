use std::{
    io::{Read, Write},
    mem::size_of,
};

use anyhow::{bail, Result};
use serde::{Deserialize, Serialize};
use tracing::trace;

#[derive(Debug, Serialize, Deserialize, PartialEq, Eq)]
pub enum Message {
    ForwardSignal(i32),
}

impl Message {
    pub fn write_to(&self, mut stream: impl Write) -> Result<()> {
        let serialized = bincode::serialize(self)?;
        if serialized.len() > u32::MAX as usize {
            bail!("serialized length too big");
        }
        trace!(len = serialized.len(), "send.");
        let len_bytes = (serialized.len() as u32).to_be_bytes();
        stream.write_all(&len_bytes)?;

        stream.write_all(&serialized)?;
        Ok(())
    }

    pub fn read_from(mut stream: impl Read) -> Result<Self> {
        let len = {
            let mut len_bytes = [0_u8; size_of::<u32>()];
            stream.read_exact(&mut len_bytes)?;
            u32::from_be_bytes(len_bytes) as usize
        };
        trace!(len, "recv.");

        let mut serialized = vec![0_u8; len];
        stream.read_exact(&mut serialized)?;

        Ok(bincode::deserialize(&serialized)?)
    }
}
