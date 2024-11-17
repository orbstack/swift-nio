use std::io::{Read, Write};

use async_trait::async_trait;
use prost::{bytes::BytesMut, Message};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use crate::asyncfile::AsyncFile;

pub mod wormhole {
    include!(concat!(env!("OUT_DIR"), "/wormhole.rs"));
}

pub const RPC_SOCKET: &str = "/run/wormhole.sock";

#[async_trait]
pub trait RpcWrite: Message + Sized {
    async fn write_all(self, stream: &mut AsyncFile) -> anyhow::Result<()>;
}

pub trait RpcWriteSync: Message + Sized {
    fn write_all_sync(self, stream: &mut impl Write) -> anyhow::Result<()>;
}

#[async_trait]
pub trait RpcRead: Message + Sized {
    async fn read(stream: &mut AsyncFile) -> anyhow::Result<Self>;
}

pub trait RpcReadSync: Message + Sized {
    fn read_sync(stream: &mut impl Read) -> anyhow::Result<Self>;
}

#[async_trait]
impl<T> RpcWrite for T
where
    T: Message + Send + Sync + 'static,
{
    async fn write_all(self, stream: &mut AsyncFile) -> anyhow::Result<()> {
        let mut buf = BytesMut::with_capacity(self.encoded_len());
        self.encode(&mut buf)?;

        let len_bytes = u32::try_from(buf.len())?.to_be_bytes();

        stream.write_all(&len_bytes).await?;
        stream.write_all(&buf).await?;
        stream.flush().await?;

        Ok(())
    }
}

impl<T> RpcWriteSync for T
where
    T: Message + Send + Sync + 'static,
{
    fn write_all_sync(self, stream: &mut impl Write) -> anyhow::Result<()> {
        let mut buf = BytesMut::with_capacity(self.encoded_len());
        self.encode(&mut buf)?;

        let len_bytes = u32::try_from(buf.len())?.to_be_bytes();

        stream.write_all(&len_bytes)?;
        stream.write_all(&buf)?;
        stream.flush()?;

        Ok(())
    }
}

#[async_trait]
impl<T> RpcRead for T
where
    T: Message + Default,
{
    async fn read(stream: &mut AsyncFile) -> anyhow::Result<T> {
        let len = {
            let mut len_bytes = [0_u8; size_of::<u32>()];
            stream.read_exact(&mut len_bytes).await?;
            u32::from_be_bytes(len_bytes) as usize
        };

        let mut data = vec![0_u8; len];
        stream.read_exact(&mut data).await?;

        T::decode(&data[..]).map_err(|e| e.into())
    }
}

impl<T> RpcReadSync for T
where
    T: Message + Default,
{
    fn read_sync(stream: &mut impl Read) -> anyhow::Result<T> {
        let len = {
            let mut len_bytes = [0_u8; size_of::<u32>()];
            stream.read_exact(&mut len_bytes)?;
            u32::from_be_bytes(len_bytes) as usize
        };

        let mut data = vec![0_u8; len];
        stream.read_exact(&mut data)?;

        T::decode(&data[..]).map_err(|e| e.into())
    }
}
