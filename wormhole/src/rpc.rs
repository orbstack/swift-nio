use anyhow::anyhow;
use libc::{epoll_wait, EWOULDBLOCK, TIOCSCTTY, TIOCSWINSZ};
use nix::{
    errno::Errno,
    fcntl::{fcntl, FcntlArg, OFlag},
    libc::ioctl,
    pty::{openpty, OpenptyResult, Winsize},
    sys::{
        epoll::{Epoll, EpollCreateFlags, EpollEvent, EpollFlags},
        termios::{tcgetattr, tcsetattr, SetArg, Termios},
    },
    unistd::{close, dup, dup2, execve, fork, pipe, setsid, sleep, ForkResult},
};
use prost::{bytes::BytesMut, Message};
use std::{
    collections::HashMap,
    ffi::CString,
    fs::File,
    io::{self, stderr, stdout, Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd, RawFd},
        unix::net::UnixStream,
    },
    path::Path,
    process,
    sync::Arc,
};
use tokio::{
    io::{unix::AsyncFd, AsyncReadExt, AsyncWrite, AsyncWriteExt},
    sync::Mutex,
    task::{self, JoinHandle},
};
use tracing::trace;
use wormhole::{rpc_client_message, rpc_server_message, StdoutData};

use crate::{
    asyncfile::AsyncFile,
    termios::create_pty,
    // model::WormholeConfig,
    // termios::{create_pty, set_termios},
};

pub mod wormhole {
    include!(concat!(env!("OUT_DIR"), "/wormhole.rs"));
}

// ideal interface

#[derive(Debug, PartialEq, Eq)]
pub enum RpcType {
    ReadStdin = 1,
    WindowChange = 2,
    RequestPty = 3,
    Start = 4,
    StartServerAck = 5,
}

impl RpcType {
    pub fn from_const(rpc_type: u8) -> Self {
        match rpc_type {
            1 => Self::ReadStdin,
            2 => Self::WindowChange,
            3 => Self::RequestPty,
            4 => Self::Start,
            5 => Self::StartServerAck,
            _ => panic!("invalid rpc type {rpc_type}"),
        }
    }
}

impl rpc_server_message::ServerMessage {
    pub async fn write(self, stream: &mut AsyncFile) -> anyhow::Result<()> {
        let mut buf = BytesMut::with_capacity(self.encoded_len());
        self.encode(&mut buf);

        let len_bytes = u32::try_from(buf.len())?.to_be_bytes();

        stream.write_all(&len_bytes).await?;
        stream.write_all(&buf).await?;
        Ok(())
    }
}
// fn write_message( rpc_client_message::Message){

// }

// fn write_to_sync(message: rpc_client_message::Message) {
//     let mut buf = BytesMut::with_capacity(greeting.encoded_len());
//     greeting.encode(&mut buf).expect("Failed to encode greeting");

//     // Get the length of the serialized message
//     let msg_len = buf.len() as u32;

//     // Create a buffer for the length prefix
//     let mut len_buf = [0u8; 4];
//     len_buf.copy_from_slice(&msg_len.to_be_bytes());

//     // Write the length prefix to the stream
//     stream.write_all(&len_buf).await?;

//     // Write the serialized message to the stream
//     stream.write_all(&buf).await?;

//     Ok(())

//     rpc_client_message::Message::StdoutData(StdoutData {
//         data: vec![1, 2, 3],
//     });
// }

// pub enum RpcOutputMessage<'a> {
//     StdioData(u8, &'a [u8]),
//     StartServer(),
//     Exit(u8),
//     ConnectServerAck(),
// }

// impl<'a> RpcOutputMessage<'a> {
//     pub fn to_const(&self) -> u8 {
//         match self {
//             Self::StdioData(_, _) => 1,
//             Self::StartServer() => 2,
//             Self::Exit(_) => 3,
//             Self::ConnectServerAck() => 4,
//         }
//     }

//     pub fn write_to_sync(&self, stream: &mut impl Write) -> anyhow::Result<()> {
//         stream.write(&[self.to_const()])?;

//         match self {
//             Self::StdioData(fd, data) => {
//                 let len_bytes = u32::try_from(data.len() + 1)?.to_be_bytes();
//                 stream.write(&len_bytes)?;
//                 stream.write(&[*fd])?;
//                 stream.write(data)?;
//             }
//             Self::StartServer() => {}
//             Self::Exit(exit_code) => {
//                 stream.write(&[*exit_code])?;
//             }
//             Self::ConnectServerAck() => {}
//         };

//         Ok(())
//     }

//     pub async fn write_to(&self, stream: &mut AsyncFile) -> anyhow::Result<()> {
//         stream.write(&[self.to_const()]).await?;

//         match self {
//             Self::StdioData(fd, data) => {
//                 trace!("writing {} bytes", data.len() + 1);
//                 let len_bytes = u32::try_from(data.len() + 1)?.to_be_bytes();
//                 trace!("len bytes {:?} bytes", len_bytes);
//                 stream.write(&len_bytes).await?;
//                 stream.write(&[*fd]).await?;
//                 stream.write(data).await?;
//             }
//             Self::StartServer() => {}
//             Self::Exit(exit_code) => {
//                 stream.write(&[*exit_code]).await?;
//             }
//             Self::ConnectServerAck() => {}
//         };

//         Ok(())
//     }
// }

#[derive(Debug)]
pub struct PtyConfig {
    pub pty: OpenptyResult,
    pub term_env: String,
}

pub enum RpcInputMessage {
    StdinData(Vec<u8>),
    TerminalResize(u16, u16),
    RequestPty(PtyConfig),
    StartPayload(String),
    StartServerAck(),
    ConnectServerAck(),
}

impl RpcInputMessage {}

fn read_bytes_sync(stream: &mut impl Read) -> anyhow::Result<Vec<u8>> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        stream.read_exact(&mut len_bytes)?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut data = vec![0_u8; len];
    stream.read_exact(&mut data)?;
    Ok(data)
}

fn read_u16_sync(stream: &mut impl Read) -> anyhow::Result<u16> {
    let mut buf = [0_u8; size_of::<u16>()];
    stream.read_exact(&mut buf)?;
    Ok(u16::from_be_bytes(buf))
}

async fn read_bytes(stream: &mut AsyncFile) -> anyhow::Result<Vec<u8>> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        stream.read_exact(&mut len_bytes).await?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut data = vec![0_u8; len];
    stream.read_exact(&mut data).await?;
    Ok(data)
}

async fn read_u16(stream: &mut AsyncFile) -> anyhow::Result<u16> {
    let mut buf = [0_u8; size_of::<u16>()];
    stream.read_exact(&mut buf).await?;
    Ok(u16::from_be_bytes(buf))
}

impl RpcInputMessage {
    pub fn read_from_sync(stream: &mut impl Read) -> anyhow::Result<Self> {
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte)?;
            RpcType::from_const(rpc_type_byte[0])
        };
        match rpc_type {
            RpcType::ReadStdin => {
                let data = read_bytes_sync(stream)?;
                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::WindowChange => {
                let h = read_u16_sync(stream)?;
                let w = read_u16_sync(stream)?;
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::RequestPty => {
                let term_env = String::from_utf8(read_bytes_sync(stream)?)?;
                let h = read_u16_sync(stream)?;
                let w = read_u16_sync(stream)?;
                let termios_config = read_bytes_sync(stream)?;
                let pty = create_pty(w, h, termios_config)?;

                Ok(RpcInputMessage::RequestPty(PtyConfig { pty, term_env }))
            }
            RpcType::Start => {
                let data = read_bytes_sync(stream)?;
                Ok(RpcInputMessage::StartPayload(String::from_utf8(data)?))
            }
            RpcType::StartServerAck => Ok(RpcInputMessage::StartServerAck()),
        }
    }

    pub async fn read_from(stream: &mut AsyncFile) -> anyhow::Result<Self> {
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte).await?;
            RpcType::from_const(rpc_type_byte[0])
        };
        match rpc_type {
            RpcType::ReadStdin => {
                let data = read_bytes(stream).await?;
                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::WindowChange => {
                let h = read_u16(stream).await?;
                let w = read_u16(stream).await?;
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::RequestPty => {
                let term_env = String::from_utf8(read_bytes(stream).await?)?;
                let h = read_u16(stream).await?;
                let w = read_u16(stream).await?;
                let termios_config = read_bytes(stream).await?;
                let pty = create_pty(w, h, termios_config)?;

                Ok(RpcInputMessage::RequestPty(PtyConfig { pty, term_env }))
            }
            RpcType::Start => {
                let data = read_bytes(stream).await?;
                Ok(RpcInputMessage::StartPayload(String::from_utf8(data)?))
            }
            RpcType::StartServerAck => Ok(RpcInputMessage::StartServerAck()),
        }
    }
}
