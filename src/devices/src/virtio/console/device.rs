use bitflags::bitflags;
use gruel::{
    define_waker_set, BoundSignalChannel, BoundSignalChannelRef, DynamicallyBoundWaker,
    SignalChannel,
};
use newt::{make_bit_flag_range, BitFlagRange};
use std::cmp;
use std::io::Write;
use std::iter::zip;
use std::mem::{size_of, size_of_val};
use std::sync::atomic::AtomicUsize;
use std::sync::Arc;
use utils::Mutex;

use libc::TIOCGWINSZ;
use utils::eventfd::EventFd;
use vm_memory::{ByteValued, Bytes, GuestMemoryMmap};

use super::super::{ActivateResult, ConsoleError, DeviceState, Queue as VirtQueue, VirtioDevice};
use super::{defs, defs::control_event, defs::uapi};
use crate::legacy::Gic;
use crate::virtio::console::console_control::{
    ConsoleControl, VirtioConsoleControl, VirtioConsoleResize,
};
use crate::virtio::console::defs::QUEUE_SIZE;
use crate::virtio::console::irq_signaler::IRQSignaler;
use crate::virtio::console::port::Port;
use crate::virtio::console::port_queue_mapping::{
    num_queues, port_id_to_queue_idx, QueueDirection,
};
use crate::virtio::{PortDescription, VirtioQueueSignals, VmmExitObserver};

define_waker_set! {
    #[derive(Default)]
    pub(crate) struct ConsoleWakers {
        dynamic: DynamicallyBoundWaker,
    }
}

bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub(crate) struct ConsoleSignalMask: u64 {
        const CONTROL_RXQ_CONTROL = 1 << 0;
        const ACTIVATE = 1 << 1;
        const SIGWINCH = 1 << 2;
        const DUMMY_INTERRUPT = 1 << 3;
        const QUEUE_EVENTS = u64::MAX << 4;
    }
}

pub(crate) const CONSOLE_QUEUE_SIGS: BitFlagRange<ConsoleSignalMask> =
    make_bit_flag_range!(mask ConsoleSignalMask::QUEUE_EVENTS);

pub(crate) const CONTROL_RXQ_INDEX: usize = 2;
pub(crate) const CONTROL_TXQ_INDEX: usize = 3;

pub(crate) const AVAIL_FEATURES: u64 = 1 << uapi::VIRTIO_CONSOLE_F_SIZE as u64
    | 1 << uapi::VIRTIO_CONSOLE_F_MULTIPORT as u64
    | 1 << uapi::VIRTIO_F_VERSION_1 as u64;

pub(crate) fn get_win_size() -> (u16, u16) {
    #[repr(C)]
    #[derive(Default)]
    struct WS {
        rows: u16,
        cols: u16,
        xpixel: u16,
        ypixel: u16,
    }
    let mut ws: WS = WS::default();

    unsafe {
        libc::ioctl(0, TIOCGWINSZ, &mut ws);
    }

    (ws.cols, ws.rows)
}

#[derive(Copy, Clone, Debug, Default)]
#[repr(C, packed)]
pub struct VirtioConsoleConfig {
    cols: u16,
    rows: u16,
    max_nr_ports: u32,
    emerg_wr: u32,
}

// Safe because it only has data and has no implicit padding.
unsafe impl ByteValued for VirtioConsoleConfig {}

impl VirtioConsoleConfig {
    pub fn new(cols: u16, rows: u16, max_nr_ports: u32) -> Self {
        VirtioConsoleConfig {
            cols,
            rows,
            max_nr_ports,
            emerg_wr: 0u32,
        }
    }
}

pub struct Console {
    pub(crate) device_state: DeviceState,
    pub(crate) irq: IRQSignaler,
    pub(crate) control: Arc<ConsoleControl>,
    pub(crate) ports: Vec<Port>,

    pub(crate) queues: Vec<VirtQueue>,
    pub(crate) signals: Arc<SignalChannel<ConsoleSignalMask, ConsoleWakers>>,

    pub(crate) avail_features: u64,
    pub(crate) acked_features: u64,

    config: VirtioConsoleConfig,
}

impl Console {
    pub fn new(ports: Vec<PortDescription>) -> super::Result<Console> {
        assert!(!ports.is_empty(), "Expected at least 1 port");
        assert!(
            matches!(ports[0], PortDescription::Console { .. }),
            "First port must be a console"
        );

        let num_queues = num_queues(ports.len());
        let queues = vec![VirtQueue::new(QUEUE_SIZE); num_queues];

        let mut queue_events = Vec::new();
        for _ in 0..queues.len() {
            queue_events
                .push(EventFd::new(utils::eventfd::EFD_NONBLOCK).map_err(ConsoleError::EventFd)?);
        }

        let (cols, rows) = get_win_size();
        let config = VirtioConsoleConfig::new(cols, rows, ports.len() as u32);
        let ports = zip(0u32.., ports)
            .map(|(port_id, description)| Port::new(port_id, description))
            .collect();

        let signals = Arc::new(SignalChannel::new(ConsoleWakers::default()));
        let control = ConsoleControl::new(BoundSignalChannel::new(
            signals.clone(),
            ConsoleSignalMask::CONTROL_RXQ_CONTROL,
        ));

        Ok(Console {
            irq: IRQSignaler::new(),
            control,
            ports,
            queues,
            signals,
            avail_features: AVAIL_FEATURES,
            acked_features: 0,
            device_state: DeviceState::Inactive,
            config,
        })
    }

    pub fn id(&self) -> &str {
        defs::CONSOLE_DEV_ID
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.irq.set_intc(intc)
    }

    pub fn update_console_size(&mut self, cols: u16, rows: u16) {
        tracing::debug!("update_console_size: {} {}", cols, rows);
        // Note that we currently only support resizing on the first/main console
        self.control
            .console_resize(0, VirtioConsoleResize { rows, cols });
    }

    pub(crate) fn process_control_rx(&mut self) -> bool {
        tracing::trace!("process_control_rx");
        let DeviceState::Activated(ref mem) = self.device_state else {
            unreachable!()
        };
        let mut raise_irq = false;

        while let Some(head) = self.queues[CONTROL_RXQ_INDEX].pop(mem) {
            if let Some(buf) = self.control.queue_pop() {
                match mem.write(&buf, head.addr) {
                    Ok(n) => {
                        if n != buf.len() {
                            tracing::error!("process_control_rx: partial write");
                        }
                        raise_irq = true;
                        tracing::trace!("process_control_rx wrote {n}");
                        if let Err(e) =
                            self.queues[CONTROL_RXQ_INDEX].add_used(mem, head.index, n as u32)
                        {
                            error!("failed to add used elements to the queue: {:?}", e);
                        }
                    }
                    Err(e) => {
                        tracing::error!("process_control_rx failed to write: {e}");
                    }
                }
            } else {
                self.queues[CONTROL_RXQ_INDEX].undo_pop();
                break;
            }
        }
        raise_irq
    }

    pub(crate) fn process_control_tx(&mut self) -> bool {
        tracing::trace!("process_control_tx");
        let DeviceState::Activated(ref mem) = self.device_state else {
            unreachable!()
        };

        let tx_queue = &mut self.queues[CONTROL_TXQ_INDEX];
        let mut raise_irq = false;

        let mut ports_to_start = Vec::new();

        while let Some(head) = tx_queue.pop(mem) {
            raise_irq = true;

            let cmd: VirtioConsoleControl = match mem.read_obj(head.addr) {
                Ok(cmd) => cmd,
                Err(e) => {
                    tracing::error!(
                    "Failed to read VirtioConsoleControl struct: {e:?}, struct len = {len}, head.len = {head_len}",
                    len = size_of::<VirtioConsoleControl>(),
                    head_len = head.len,
                );
                    continue;
                }
            };
            if let Err(e) = tx_queue.add_used(mem, head.index, size_of_val(&cmd) as u32) {
                error!("failed to add used elements to the queue: {:?}", e);
            }

            tracing::trace!("VirtioConsoleControl cmd: {cmd:?}");
            match cmd.event {
                control_event::VIRTIO_CONSOLE_DEVICE_READY => {
                    tracing::debug!(
                        "Device is ready: initialization {}",
                        if cmd.value == 1 { "ok" } else { "failed" }
                    );
                    for port_id in 0..self.ports.len() {
                        self.control.port_add(port_id as u32);
                    }
                }
                control_event::VIRTIO_CONSOLE_PORT_READY => {
                    if cmd.value != 1 {
                        tracing::error!("Port initialization failed: {:?}", cmd);
                        continue;
                    }

                    if self.ports[cmd.id as usize].is_console() {
                        self.control.mark_console_port(mem, cmd.id);
                        self.control.port_open(cmd.id, true);
                    } else {
                        // We start with all ports open, this makes sense for now,
                        // because underlying file descriptors STDIN, STDOUT, STDERR are always open too
                        self.control.port_open(cmd.id, true)
                    }

                    let name = self.ports[cmd.id as usize].name();
                    tracing::trace!("Port ready {id}: {name}", id = cmd.id);
                    if !name.is_empty() {
                        self.control.port_name(cmd.id, name)
                    }
                }
                control_event::VIRTIO_CONSOLE_PORT_OPEN => {
                    let opened = match cmd.value {
                        0 => false,
                        1 => true,
                        _ => {
                            tracing::error!(
                                "Invalid value ({}) for VIRTIO_CONSOLE_PORT_OPEN on port {}",
                                cmd.value,
                                cmd.id
                            );
                            continue;
                        }
                    };

                    if !opened {
                        tracing::debug!("Guest closed port {}", cmd.id);
                        continue;
                    }

                    ports_to_start.push(cmd.id as usize);
                }
                _ => tracing::warn!("Unknown console control event {:x}", cmd.event),
            }
        }

        for port_id in ports_to_start {
            tracing::trace!("Starting port io for port {}", port_id);
            self.ports[port_id].start(
                mem.clone(),
                self.queues[port_id_to_queue_idx(QueueDirection::Rx, port_id)].clone(),
                self.queues[port_id_to_queue_idx(QueueDirection::Tx, port_id)].clone(),
                self.irq.clone(),
                self.control.clone(),
            );
        }

        raise_irq
    }
}

impl VirtioDevice for Console {
    fn avail_features(&self) -> u64 {
        self.avail_features
    }

    fn acked_features(&self) -> u64 {
        self.acked_features
    }

    fn set_acked_features(&mut self, acked_features: u64) {
        self.acked_features = acked_features
    }

    fn device_type(&self) -> u32 {
        uapi::VIRTIO_ID_CONSOLE
    }

    fn queues(&self) -> &[VirtQueue] {
        &self.queues
    }

    fn queues_mut(&mut self) -> &mut [VirtQueue] {
        &mut self.queues
    }

    fn queue_signals(&self) -> VirtioQueueSignals {
        VirtioQueueSignals::new(self.signals.clone(), CONSOLE_QUEUE_SIGS)
    }

    fn interrupt_signal(&self) -> BoundSignalChannelRef<'_> {
        BoundSignalChannel::new(&*self.signals, ConsoleSignalMask::DUMMY_INTERRUPT)
    }

    fn interrupt_status(&self) -> Arc<AtomicUsize> {
        self.irq.interrupt_status()
    }

    fn set_irq_line(&mut self, irq: u32) {
        self.irq.set_irq_line(irq)
    }

    fn read_config(&self, offset: u64, mut data: &mut [u8]) {
        let config_slice = self.config.as_slice();
        let config_len = config_slice.len() as u64;
        if offset >= config_len {
            error!("Failed to read config space");
            return;
        }
        if let Some(end) = offset.checked_add(data.len() as u64) {
            // This write can't fail, offset and end are checked against config_len.
            data.write_all(&config_slice[offset as usize..cmp::min(end, config_len) as usize])
                .unwrap();
        }
    }

    fn write_config(&mut self, offset: u64, data: &[u8]) {
        warn!(
            "console: guest driver attempted to write device config (offset={:x}, len={:x})",
            offset,
            data.len()
        );
    }

    fn activate(&mut self, mem: GuestMemoryMmap) -> ActivateResult {
        self.signals.assert(ConsoleSignalMask::ACTIVATE);
        self.device_state = DeviceState::Activated(mem);

        Ok(())
    }

    fn is_activated(&self) -> bool {
        match self.device_state {
            DeviceState::Inactive => false,
            DeviceState::Activated(_) => true,
        }
    }

    fn reset(&mut self) -> bool {
        // Strictly speaking, we should also unsubscribe the queue
        // events, resubscribe the activate eventfd and deactivate
        // the device, but we don't support any scenario in which
        // neither GuestMemory nor the queue events would change,
        // so let's avoid doing any unnecessary work.
        for port in &mut self.ports {
            port.shutdown();
        }
        true
    }
}

impl VmmExitObserver for Console {
    fn on_vmm_exit(&mut self) {
        self.reset();
        tracing::trace!("Console on_vmm_exit finished");
    }
}
