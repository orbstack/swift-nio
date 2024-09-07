use gruel::SignalChannel;
use std::borrow::Cow;
use std::sync::Arc;
use std::thread::JoinHandle;
use std::{mem, thread};
use utils::memory::GuestMemory;
use utils::Mutex;

use crate::virtio::console::irq_signaler::IRQSignaler;
use crate::virtio::console::port_io::{PortInput, PortOutput};
use crate::virtio::Queue;

use super::device::{PortSignalMask, PortWakers};
use super::port_worker::PortWorker;

pub enum PortDescription {
    Console {
        input: Option<Box<dyn PortInput + Send>>,
        output: Option<Box<dyn PortOutput + Send>>,
    },
    InputPipe {
        name: Cow<'static, str>,
        input: Box<dyn PortInput + Send>,
    },
    OutputPipe {
        name: Cow<'static, str>,
        output: Box<dyn PortOutput + Send>,
    },
}

enum PortState {
    Inactive,
    Active(JoinHandle<()>),
}

pub(crate) struct Port {
    pub(crate) port_id: u32,
    /// Empty if no name given
    name: Cow<'static, str>,
    represents_console: bool,
    pub(crate) signal: Arc<SignalChannel<PortSignalMask, PortWakers>>,
    state: PortState,
    input: Option<Arc<Mutex<Box<dyn PortInput + Send>>>>,
    pub(crate) output: Option<Arc<Mutex<Box<dyn PortOutput + Send>>>>,
}

impl Port {
    pub(crate) fn new(port_id: u32, description: PortDescription) -> Self {
        match description {
            PortDescription::Console { input, output } => Self {
                port_id,
                name: "".into(),
                represents_console: true,
                signal: Arc::new(SignalChannel::new(PortWakers::default())),
                state: PortState::Inactive,
                input: Some(Arc::new(Mutex::new(input.unwrap()))),
                output: Some(Arc::new(Mutex::new(output.unwrap()))),
            },
            PortDescription::InputPipe { name, input } => Self {
                port_id,
                name,
                represents_console: false,
                signal: Arc::new(SignalChannel::new(PortWakers::default())),
                state: PortState::Inactive,
                input: Some(Arc::new(Mutex::new(input))),
                output: None,
            },
            PortDescription::OutputPipe { name, output } => Self {
                port_id,
                name,
                represents_console: false,
                signal: Arc::new(SignalChannel::new(PortWakers::default())),
                state: PortState::Inactive,
                input: None,
                output: Some(Arc::new(Mutex::new(output))),
            },
        }
    }

    pub fn name(&self) -> &str {
        &self.name
    }

    pub fn is_console(&self) -> bool {
        self.represents_console
    }

    pub fn start(
        &mut self,
        mem: GuestMemory,
        rx_queue: Queue,
        tx_queue: Queue,
        irq_signaler: IRQSignaler,
    ) {
        if let PortState::Active(_) = &mut self.state {
            self.shutdown();
        };

        let input = self.input.as_ref().cloned();
        let output = self.output.as_ref().cloned();

        let mut worker = PortWorker::new(
            mem,
            rx_queue,
            tx_queue,
            irq_signaler,
            self.signal.clone(),
            input,
            output,
        );

        let thread = thread::Builder::new()
            .name(format!("console port {}", self.port_id))
            .spawn(move || worker.run())
            .expect("failed to spawn thread");

        self.state = PortState::Active(thread);
    }

    pub fn shutdown(&mut self) {
        if let PortState::Active(handle) = mem::replace(&mut self.state, PortState::Inactive) {
            self.signal.assert(PortSignalMask::STOP);
            handle.join().expect("failed to join thread");
        }
    }
}
