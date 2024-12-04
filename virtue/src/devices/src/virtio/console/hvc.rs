use std::{io, sync::Arc};

use anyhow::anyhow;
use bytemuck::{Pod, Zeroable};
use utils::{
    hypercalls::{HVC_DEVICE_CONSOLE_START, ORBVM_CONSOLE_REQ_WRITE, SMCCC_RET_INVALID_PARAMETER},
    memory::{GuestAddress, GuestMemory},
    Mutex,
};

use crate::hvc::HvcDevice;

use super::port_io::PortOutput;

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct OrbvmConsoleReq {
    type_: u16,
    _pad0: [u16; 3],
    addr: GuestAddress,
    len: u64,
}

pub struct ConsoleHvcDevice {
    mem: GuestMemory,
    port_id: u32,
    output: Option<Arc<Mutex<Box<dyn PortOutput + Send>>>>,
}

impl ConsoleHvcDevice {
    pub fn new(
        mem: GuestMemory,
        port_id: u32,
        output: Option<Arc<Mutex<Box<dyn PortOutput + Send>>>>,
    ) -> Self {
        Self {
            mem,
            port_id,
            output,
        }
    }

    fn handle_hvc(&self, args_addr: GuestAddress) -> anyhow::Result<i64> {
        if let Some(output) = &self.output {
            let req = self.mem.read::<OrbvmConsoleReq>(args_addr)?;
            if req.type_ != ORBVM_CONSOLE_REQ_WRITE {
                return Err(anyhow!("invalid request type"));
            }

            let vs = self.mem.get_slice(req.addr, req.len as usize)?;
            let mut output = output.lock().unwrap();
            match output.write_volatile(vs) {
                Ok(_) => {}

                Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                    // EAGAIN = use standard blocking / spinning virtio path
                    // any error will cause Linux to retry
                    return Ok(SMCCC_RET_INVALID_PARAMETER);
                }

                Err(e) => {
                    return Err(e.into());
                }
            }
        }

        Ok(0)
    }
}

impl HvcDevice for ConsoleHvcDevice {
    fn call_hvc(&self, args_addr: GuestAddress) -> i64 {
        match self.handle_hvc(args_addr) {
            Ok(_) => 0,
            Err(e) => {
                error!("failed to handle hvc: {:?}", e);
                -1
            }
        }
    }

    fn hvc_id(&self) -> Option<usize> {
        Some(HVC_DEVICE_CONSOLE_START + self.port_id as usize)
    }
}
