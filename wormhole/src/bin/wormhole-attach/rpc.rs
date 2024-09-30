use std::io::{Read, Write};

#[derive(Debug, PartialEq, Eq)]
pub enum RpcType {
    StdinData = 1,
    TerminalResize = 2,
    TermiosSettings = 3,
}

impl RpcType {
    pub fn from_const(rpc_type: u8) -> Self {
        match rpc_type {
            1 => Self::StdinData,
            2 => Self::TerminalResize,
            3 => Self::TermiosSettings,
            _ => panic!("invalid rpc type {rpc_type}"),
        }
    }
}

pub enum RpcOutputMessage<'a> {
    StdData(&'a [u8]),
    Exit(u8),
}

impl<'a> RpcOutputMessage<'a> {
    pub fn to_const(&self) -> u8 {
        match self {
            Self::StdData(_) => 1,
            Self::Exit(_) => 2,
        }
    }

    pub fn write_to(&self, stream: &mut impl Write) -> anyhow::Result<()> {
        stream.write_all(&[self.to_const()])?;

        match self {
            Self::StdData(data) => {
                let len_bytes = u32::try_from(data.len())?.to_be_bytes();
                stream.write_all(&len_bytes)?;
                stream.write_all(data)?;
            }
            Self::Exit(exit_code) => stream.write_all(&[*exit_code])?,
        };

        Ok(())
    }
}

pub enum RpcInputMessage {
    StdinData(Vec<u8>),
    TerminalResize(u16, u16),
    TermiosSettings(),
}

impl RpcInputMessage {
    pub fn read_from(stream: &mut impl Read) -> anyhow::Result<Self> {
        let rpc_type = {
            let mut rpc_type_byte = [0u8];
            stream.read_exact(&mut rpc_type_byte)?;
            RpcType::from_const(rpc_type_byte[0])
        };
        match rpc_type {
            RpcType::StdinData => {
                let len = {
                    let mut len_bytes = [0_u8; size_of::<u32>()];
                    stream.read_exact(&mut len_bytes)?;
                    u32::from_be_bytes(len_bytes) as usize
                };

                let mut data = vec![0_u8; len];
                stream.read_exact(&mut data)?;

                Ok(RpcInputMessage::StdinData(data))
            }
            RpcType::TerminalResize => {
                let mut buf = [0_u8; size_of::<u16>()];
                stream.read_exact(&mut buf)?;
                let w = u16::from_be_bytes(buf);

                stream.read_exact(&mut buf)?;
                let h = u16::from_be_bytes(buf);
                Ok(RpcInputMessage::TerminalResize(w, h))
            }
            RpcType::TermiosSettings => {
                todo!("not yet supported")
            }
        }
    }
}
