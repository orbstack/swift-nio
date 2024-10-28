use prost::{bytes::BytesMut, Message};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use wormhole::{rpc_client_message::ClientMessage, RpcClientMessage, RpcServerMessage};

use crate::asyncfile::AsyncFile;

pub mod wormhole {
    include!(concat!(env!("OUT_DIR"), "/wormhole.rs"));
}

impl RpcServerMessage {
    pub async fn write(self, stream: &mut AsyncFile) -> anyhow::Result<()> {
        let mut buf = BytesMut::with_capacity(self.encoded_len());
        self.encode(&mut buf)?;

        let len_bytes = u32::try_from(buf.len())?.to_be_bytes();

        stream.write_all(&len_bytes).await?;
        stream.write_all(&buf).await?;

        Ok(())
    }
}

impl RpcClientMessage {
    pub async fn read(stream: &mut AsyncFile) -> anyhow::Result<ClientMessage> {
        let len = {
            let mut len_bytes = [0_u8; size_of::<u32>()];
            stream.read_exact(&mut len_bytes).await?;
            u32::from_be_bytes(len_bytes) as usize
        };

        let mut data = vec![0_u8; len];
        stream.read_exact(&mut data).await?;

        let msg = RpcClientMessage::decode(&data[..])?;
        Ok(msg.client_message.expect("expected client message"))
    }
}
