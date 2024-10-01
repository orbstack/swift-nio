use std::io::{Read, Write};

use nix::{
    pty::{openpty, OpenptyResult, Winsize},
    sys::termios::{tcgetattr, tcsetattr, SetArg, Termios},
};

use crate::termios::{create_pty, set_termios};

#[derive(Debug, PartialEq, Eq)]
pub enum RpcType {
    ReadStdin = 1,
    WindowChange = 2,
    RequestPty = 3,
    Start = 4,
}

impl RpcType {
    pub fn from_const(rpc_type: u8) -> Self {
        match rpc_type {
            1 => Self::ReadStdin,
            2 => Self::WindowChange,
            3 => Self::RequestPty,
            4 => Self::Start,
            _ => panic!("invalid rpc type {rpc_type}"),
        }
    }
}

pub enum RpcOutputMessage<'a> {
    StdioData(&'a [u8]),
    Exit(u8),
}

impl<'a> RpcOutputMessage<'a> {
    pub fn to_const(&self) -> u8 {
        match self {
            Self::StdioData(_) => 1,
            Self::Exit(_) => 2,
        }
    }

    pub fn write_to(&self, stream: &mut impl Write) -> anyhow::Result<()> {
        stream.write_all(&[self.to_const()])?;

        match self {
            Self::StdioData(data) => {
                let len_bytes = u32::try_from(data.len())?.to_be_bytes();
                stream.write_all(&len_bytes)?;
                stream.write_all(data)?;
            }
            Self::Exit(exit_code) => stream.write_all(&[*exit_code])?,
        };

        Ok(())
    }
}

#[derive(Debug)]
pub struct PtyConfig {
    pub pty: OpenptyResult,
    pub term_env: String,
}

pub enum RpcInputMessage {
    StdinData(Vec<u8>),
    TerminalResize(u16, u16),
    RequestPty(PtyConfig),
    Start(),
}

fn read_bytes(stream: &mut impl Read) -> anyhow::Result<Vec<u8>> {
    let len = {
        let mut len_bytes = [0_u8; size_of::<u32>()];
        stream.read_exact(&mut len_bytes)?;
        u32::from_be_bytes(len_bytes) as usize
    };

    let mut data = vec![0_u8; len];
    stream.read_exact(&mut data)?;
    Ok(data)
}

fn read_u16(stream: &mut impl Read) -> anyhow::Result<u16> {
    let mut buf = [0_u8; size_of::<u16>()];
    stream.read_exact(&mut buf)?;
    Ok(u16::from_be_bytes(buf))
}

impl RpcInputMessage {
    pub fn read_from(stream: &mut impl Read) -> anyhow::Result<Self> {
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte)?;
            RpcType::from_const(rpc_type_byte[0])
        };
        match rpc_type {
            RpcType::ReadStdin => {
                let data = read_bytes(stream)?;
                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::WindowChange => {
                let h = read_u16(stream)?;
                let w = read_u16(stream)?;
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::RequestPty => {
                let term_env = String::from_utf8(read_bytes(stream)?)?;
                let h = read_u16(stream)?;
                let w = read_u16(stream)?;
                let termios_config = read_bytes(stream)?;
                let pty = create_pty(w, h, termios_config)?;

                Ok(RpcInputMessage::RequestPty(PtyConfig { pty, term_env }))
            }
            RpcType::Start => Ok(RpcInputMessage::Start()),
        }
    }
}
