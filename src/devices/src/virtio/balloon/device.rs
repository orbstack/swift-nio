use gruel::{define_waker_set, BoundSignalChannelRef, DynamicallyBoundWaker, SignalChannel};
use newt::{define_num_enum, make_bit_flag_range, BitFlagRange, NumEnumMap};
use std::cmp;
use std::convert::TryInto;
use std::io::Write;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use utils::Mutex;

use vm_memory::{ByteValued, GuestMemory, GuestMemoryMmap};

use super::super::{
    ActivateResult, DeviceState, Queue as VirtQueue, VirtioDevice, VIRTIO_MMIO_INT_VRING,
};
use super::{defs, defs::uapi};
use crate::legacy::Gic;
use crate::virtio::VirtioQueueSignals;
use hvf::Parkable;

define_waker_set! {
    #[derive(Default)]
    pub struct BalloonDeviceWakers {
        pub dynamic: DynamicallyBoundWaker,
    }
}

bitflags::bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub struct BalloonDeviceSignalMask: u64 {
        // Inflate queue.
        const IFQ = 1 << 0;

        // Deflate queue.
        const DFQ = 1 << 1;

        // Stats queue.
        const STQ = 1 << 2;

        // Page-hinting queue.
        const PHQ = 1 << 3;

        // Free page reporting queue.
        const FRQ = 1 << 4;

        const INTERRUPT = 1 << 5;
    }
}

pub const BALLOON_DEVICE_QUEUE_RANGE: BitFlagRange<BalloonDeviceSignalMask> =
    make_bit_flag_range!([
        BalloonDeviceSignalMask::IFQ,
        BalloonDeviceSignalMask::DFQ,
        BalloonDeviceSignalMask::STQ,
        BalloonDeviceSignalMask::PHQ,
        BalloonDeviceSignalMask::FRQ,
    ]);

define_num_enum! {
    pub enum BalloonDeviceQueues {
        IFQ,
        DFQ,
        STQ,
        PHQ,
        FRQ,
    }
}

// Supported features.
pub(crate) const AVAIL_FEATURES: u64 = 1 << uapi::VIRTIO_F_VERSION_1 as u64
    | 1 << uapi::VIRTIO_BALLOON_F_STATS_VQ as u64
    | 1 << uapi::VIRTIO_BALLOON_F_FREE_PAGE_HINT as u64
    | 1 << uapi::VIRTIO_BALLOON_F_REPORTING as u64;

#[derive(Copy, Clone, Debug, Default)]
#[repr(C, packed)]
pub struct VirtioBalloonConfig {
    /* Number of pages host wants Guest to give up. */
    num_pages: u32,
    /* Number of pages we've actually got in balloon. */
    actual: u32,
    /* Free page report command id, readonly by guest */
    free_page_report_cmd_id: u32,
    /* Stores PAGE_POISON if page poisoning is in use */
    poison_val: u32,
}

// Safe because it only has data and has no implicit padding.
unsafe impl ByteValued for VirtioBalloonConfig {}

pub struct Balloon {
    pub(crate) signal: Arc<SignalChannel<BalloonDeviceSignalMask, BalloonDeviceWakers>>,
    pub(crate) queues: NumEnumMap<BalloonDeviceQueues, VirtQueue>,
    pub(crate) avail_features: u64,
    pub(crate) acked_features: u64,
    pub(crate) interrupt_status: Arc<AtomicUsize>,
    pub(crate) device_state: DeviceState,
    config: VirtioBalloonConfig,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,
    parker: Option<Arc<dyn Parkable>>,
}

impl Balloon {
    pub(crate) fn with_queues(queues: Vec<VirtQueue>) -> super::Result<Balloon> {
        let config = VirtioBalloonConfig::default();

        Ok(Balloon {
            signal: Arc::new(SignalChannel::new(BalloonDeviceWakers::default())),
            queues: NumEnumMap::from_iter(queues),
            avail_features: AVAIL_FEATURES,
            acked_features: 0,
            interrupt_status: Arc::new(AtomicUsize::new(0)),
            device_state: DeviceState::Inactive,
            config,
            intc: None,
            irq_line: None,
            parker: None,
        })
    }

    pub fn new() -> super::Result<Balloon> {
        let queues: Vec<VirtQueue> = defs::QUEUE_SIZES
            .iter()
            .map(|&max_size| VirtQueue::new(max_size))
            .collect();
        Self::with_queues(queues)
    }

    pub fn id(&self) -> &str {
        defs::BALLOON_DEV_ID
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.intc = Some(intc);
    }

    pub fn set_parker(&mut self, parker: Arc<dyn Parkable>) {
        self.parker = Some(parker);
    }

    pub fn signal_used_queue(&self) {
        debug!("balloon: raising IRQ");

        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);

        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
        } else {
            self.signal.assert(BalloonDeviceSignalMask::INTERRUPT);
        }
    }

    pub fn process_frq(&mut self) -> bool {
        debug!("balloon: process_frq()");
        let mem = match self.device_state {
            DeviceState::Activated(ref mem) => mem,
            // This should never happen, it's been already validated in the event handler.
            DeviceState::Inactive => unreachable!(),
        };

        let mut have_used = false;

        while let Some(head) = self.queues[BalloonDeviceQueues::FRQ].pop(mem) {
            have_used = true;

            // the idea:
            // to work around macos bug,
            // force vcpus to exit and park them
            // hv_vm_unmap
            // madvise
            // remap
            // and unpark
            let Ok(unpark_task) = self.parker.as_ref().unwrap().park() else {
                break;
            };

            let index = head.index;
            for desc in head.into_iter() {
                let host_addr = mem.get_host_address(desc.addr).unwrap();
                debug!(
                    "balloon: should release guest_addr={:?} host_addr={:p} len={}",
                    desc.addr, host_addr, desc.len
                );
                unsafe {
                    let res = libc::madvise(
                        host_addr as *mut libc::c_void,
                        desc.len.try_into().unwrap(),
                        libc::MADV_FREE_REUSABLE,
                    );
                    debug!("ballon res = {:?}", res);
                };
            }

            self.parker.as_ref().unwrap().unpark(unpark_task);
            if let Err(e) = self.queues[BalloonDeviceQueues::FRQ].add_used(mem, index, 0) {
                error!("failed to add used elements to the queue: {:?}", e);
            }
        }

        have_used
    }
}

impl VirtioDevice for Balloon {
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
        uapi::VIRTIO_ID_BALLOON
    }

    fn queues(&self) -> &[VirtQueue] {
        &self.queues.0
    }

    fn queues_mut(&mut self) -> &mut [VirtQueue] {
        &mut self.queues.0
    }

    fn queue_signals(&self) -> VirtioQueueSignals {
        VirtioQueueSignals::new(self.signal.clone(), BALLOON_DEVICE_QUEUE_RANGE)
    }

    fn interrupt_signal(&self) -> BoundSignalChannelRef<'_> {
        BoundSignalChannelRef::new(&*self.signal, BalloonDeviceSignalMask::INTERRUPT)
    }

    fn interrupt_status(&self) -> Arc<AtomicUsize> {
        self.interrupt_status.clone()
    }

    fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(irq);
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
            "balloon: guest driver attempted to write device config (offset={:x}, len={:x})",
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
}
