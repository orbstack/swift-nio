use bytemuck::{Pod, Zeroable};
use gruel::ArcBoundSignalChannel;
use std::collections::VecDeque;
use std::ops::Deref;
use std::sync::Arc;
use utils::{memory::GuestMemory, Mutex};

use crate::virtio::console::defs::control_event::{
    VIRTIO_CONSOLE_CONSOLE_PORT, VIRTIO_CONSOLE_PORT_ADD, VIRTIO_CONSOLE_PORT_NAME,
    VIRTIO_CONSOLE_PORT_OPEN, VIRTIO_CONSOLE_RESIZE,
};

// NOTE, we rely on CPU being little endian, for the values to be correct
#[derive(Debug, Copy, Clone, Default, Pod, Zeroable)]
#[repr(C, packed(4))]
pub struct VirtioConsoleControl {
    /// Port number
    pub id: u32,
    /// The kind of control event
    pub event: u16,
    /// Extra information for the event
    pub value: u16,
}

// NOTE, we rely on CPU being little endian, for the values to be correct
#[derive(Debug, Copy, Clone, Default, Pod, Zeroable)]
#[repr(C, packed)]
pub struct VirtioConsoleResize {
    // NOTE: the order of these fields in the actual kernel implementation and in the spec are swapped,
    // we follow the order in the kernel to get it working correctly
    pub rows: u16,
    pub cols: u16,
}

pub enum Payload {
    ConsoleControl(VirtioConsoleControl),
    Bytes(Vec<u8>),
}

impl Deref for Payload {
    type Target = [u8];

    fn deref(&self) -> &Self::Target {
        match self {
            Payload::ConsoleControl(b) => bytemuck::bytes_of(b),
            Payload::Bytes(b) => b.as_slice(),
        }
    }
}

// Utility for sending commands into control rx queue
pub struct ConsoleControl {
    queue: Mutex<VecDeque<Payload>>,
    control_rxq_control: ArcBoundSignalChannel,
}

impl ConsoleControl {
    pub fn new(control_rxq_control: ArcBoundSignalChannel) -> Arc<Self> {
        Arc::new(Self {
            queue: Default::default(),
            control_rxq_control,
        })
    }

    pub fn mark_console_port(&self, _mem: &GuestMemory, port_id: u32) {
        self.push_msg(VirtioConsoleControl {
            id: port_id,
            event: VIRTIO_CONSOLE_CONSOLE_PORT,
            value: 1,
        })
    }

    #[allow(dead_code)]
    pub fn console_resize(&self, port_id: u32, new_size: VirtioConsoleResize) {
        let mut buf = Vec::new();
        buf.extend(bytemuck::bytes_of(&VirtioConsoleControl {
            id: port_id,
            event: VIRTIO_CONSOLE_RESIZE,
            value: 0,
        }));
        buf.extend(bytemuck::bytes_of(&new_size));
        self.push_vec(buf)
    }

    /// Adds another port with the specified port_id
    pub fn port_add(&self, port_id: u32) {
        self.push_msg(VirtioConsoleControl {
            id: port_id,
            event: VIRTIO_CONSOLE_PORT_ADD,
            value: 0,
        })
    }

    pub fn port_open(&self, port_id: u32, open: bool) {
        self.push_msg(VirtioConsoleControl {
            id: port_id,
            event: VIRTIO_CONSOLE_PORT_OPEN,
            value: open as u16,
        })
    }

    pub fn port_name(&self, port_id: u32, name: &str) {
        let mut buf: Vec<u8> = Vec::new();

        buf.extend_from_slice(bytemuck::bytes_of(&VirtioConsoleControl {
            id: port_id,
            event: VIRTIO_CONSOLE_PORT_NAME,
            value: 1, // Unspecified/unused in the spec, lets use the same value as QEMU.
        }));

        // The spec says the name shouldn't be NUL terminated.
        buf.extend(name.as_bytes());
        self.push_vec(buf)
    }

    pub fn queue_pop(&self) -> Option<Payload> {
        let mut queue = self.queue.lock().expect("Poisoned lock");
        queue.pop_front()
    }

    fn push_msg(&self, msg: VirtioConsoleControl) {
        let mut queue = self.queue.lock().expect("Poisoned lock");
        queue.push_back(Payload::ConsoleControl(msg));
        self.control_rxq_control.assert();
    }

    fn push_vec(&self, buf: Vec<u8>) {
        let mut queue = self.queue.lock().expect("Poisoned lock");
        queue.push_back(Payload::Bytes(buf));
        self.control_rxq_control.assert();
    }
}
