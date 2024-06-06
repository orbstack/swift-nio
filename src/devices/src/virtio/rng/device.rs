use bitflags::bitflags;
use gruel::{define_waker_set, BoundSignalChannelRef, DynamicallyBoundWaker, SignalChannel};
use newt::{define_num_enum, make_bit_flag_range, NumEnumMap};
use std::result;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use utils::Mutex;

use rand::{rngs::OsRng, RngCore};
use vm_memory::{Bytes, GuestMemoryMmap};

use super::super::{
    ActivateResult, DeviceState, Queue as VirtQueue, VirtioDevice, VIRTIO_MMIO_INT_VRING,
};
use super::{defs, defs::uapi};
use crate::legacy::Gic;
use crate::virtio::VirtioQueueSignals;
use crate::Error as DeviceError;

define_waker_set! {
    #[derive(Default)]
    pub(crate) struct RngWakers {
        dynamic: DynamicallyBoundWaker,
    }
}

bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub(crate) struct RngSignalMask: u64 {
        const INTERRUPT = 1 << 0;
        const REQ_QUEUE = 1 << 1;
    }
}

define_num_enum! {
    pub enum RngQueues {
        Request,
    }
}

// Supported features.
pub(crate) const AVAIL_FEATURES: u64 = 1 << uapi::VIRTIO_F_VERSION_1 as u64;

#[derive(Copy, Clone, Debug, Default)]
#[repr(C, packed)]
pub struct VirtioRng {}

pub struct Rng {
    pub(crate) queues: NumEnumMap<RngQueues, VirtQueue>,
    pub(crate) signals: Arc<SignalChannel<RngSignalMask, RngWakers>>,
    pub(crate) avail_features: u64,
    pub(crate) acked_features: u64,
    pub(crate) interrupt_status: Arc<AtomicUsize>,
    pub(crate) device_state: DeviceState,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,
}

impl Rng {
    pub(crate) fn with_queues(queues: Vec<VirtQueue>) -> super::Result<Rng> {
        Ok(Rng {
            queues: queues.into_iter().collect(),
            signals: Arc::new(SignalChannel::new(RngWakers::default())),
            avail_features: AVAIL_FEATURES,
            acked_features: 0,
            interrupt_status: Arc::new(AtomicUsize::new(0)),
            device_state: DeviceState::Inactive,
            intc: None,
            irq_line: None,
        })
    }

    pub fn new() -> super::Result<Rng> {
        let queues: Vec<VirtQueue> = defs::QUEUE_SIZES
            .iter()
            .map(|&max_size| VirtQueue::new(max_size))
            .collect();
        Self::with_queues(queues)
    }

    pub fn id(&self) -> &str {
        defs::RNG_DEV_ID
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.intc = Some(intc);
    }

    pub fn signal_used_queue(&self) -> result::Result<(), DeviceError> {
        debug!("rng: raising IRQ");
        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);
        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
            Ok(())
        } else {
            self.signals.assert(RngSignalMask::INTERRUPT);
            Ok(())
        }
    }

    pub fn process_req(&mut self) -> bool {
        debug!("rng: process_req()");
        let mem = match self.device_state {
            DeviceState::Activated(ref mem) => mem,
            // This should never happen, it's been already validated in the event handler.
            DeviceState::Inactive => unreachable!(),
        };

        let mut have_used = false;

        while let Some(head) = self.queues[RngQueues::Request].pop(mem) {
            let index = head.index;
            let mut written = 0;
            for desc in head.into_iter() {
                let mut rand_bytes = vec![0u8; desc.len as usize];
                OsRng.fill_bytes(&mut rand_bytes);
                if let Err(e) = mem.write_slice(&rand_bytes[..], desc.addr) {
                    error!("Failed to write slice: {:?}", e);
                    self.queues[RngQueues::Request].go_to_previous_position();
                    break;
                }
                written += desc.len;
            }

            have_used = true;
            if let Err(e) = self.queues[RngQueues::Request].add_used(mem, index, written) {
                error!("failed to add used elements to the queue: {:?}", e);
            }
        }

        have_used
    }
}

impl VirtioDevice for Rng {
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
        uapi::VIRTIO_ID_RNG
    }

    fn queues(&self) -> &[VirtQueue] {
        &self.queues.0
    }

    fn queues_mut(&mut self) -> &mut [VirtQueue] {
        &mut self.queues.0
    }

    fn queue_signals(&self) -> VirtioQueueSignals {
        VirtioQueueSignals::new(
            self.signals.clone(),
            make_bit_flag_range!([RngSignalMask::REQ_QUEUE]),
        )
    }

    fn interrupt_signal(&self) -> BoundSignalChannelRef<'_> {
        BoundSignalChannelRef::new(&*self.signals, RngSignalMask::INTERRUPT)
    }

    fn interrupt_status(&self) -> Arc<AtomicUsize> {
        self.interrupt_status.clone()
    }

    fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(irq);
    }

    fn read_config(&self, _offset: u64, _data: &mut [u8]) {
        error!("rng: invalid request to read config space");
    }

    fn write_config(&mut self, offset: u64, data: &[u8]) {
        warn!(
            "rng: guest driver attempted to write device config (offset={:x}, len={:x})",
            offset,
            data.len()
        );
    }

    fn activate(&mut self, mem: GuestMemoryMmap) -> ActivateResult {
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
        // Strictly speaking, we should unsubscribe the queue events resubscribe
        // the activate eventfd and deactivate the device, but we don't support
        // any scenario in which neither GuestMemory nor the queue events would
        // change, so let's avoid doing any unnecessary work.
        true
    }
}
