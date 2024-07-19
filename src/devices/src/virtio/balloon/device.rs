use anyhow::anyhow;
use bitfield::bitfield;
use gruel::{
    define_waker_set, BoundSignalChannelRef, ParkSignalChannelExt, ParkWaker, SignalChannel,
};
use newt::{define_num_enum, make_bit_flag_range, NumEnumMap};
use std::cmp;
use std::collections::VecDeque;
use std::io::Write;
use std::mem::size_of;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Weak};
use std::thread;
use std::time::Instant;
use utils::hypercalls::HVC_DEVICE_BALLOON;
use utils::Mutex;

use vm_memory::{ByteValued, Bytes, GuestAddress, GuestMemory, GuestMemoryMmap};

use super::super::{
    ActivateResult, DeviceState, Queue as VirtQueue, VirtioDevice, VIRTIO_MMIO_INT_VRING,
};
use super::{defs, defs::uapi};
use crate::legacy::Gic;
use crate::virtio::{DescriptorChain, HvcDevice, VirtioQueueSignals, VmmExitObserver};
use hvf::Parkable;

define_waker_set! {
    #[derive(Default)]
    pub(crate) struct BalloonWakers {
        pub parker: ParkWaker,
    }
}

bitflags::bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub(crate) struct BalloonSignalMask: u64 {
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

        // async reuse submission
        // guest doesn't need a completion IRQ for this, but it must be processed before FRQ
        const REUSE = 1 << 5;

        const INTERRUPT = 1 << 6;

        const SHUTDOWN_WORKER = 1 << 7;
    }
}

define_num_enum! {
    pub(crate) enum BalloonQueues {
        IFQ,
        DFQ,
        STQ,
        PHQ,
        FRQ,
    }
}

// kernel: orbvm_fpr_request
#[derive(Copy, Clone, Debug)]
#[repr(C)]
struct OrbvmFprRequest {
    type_: u32,
    guest_page_size: u32,

    // only for HVC
    descs_addr: u64,
    nr_descs: u32,
}

unsafe impl ByteValued for OrbvmFprRequest {}

bitfield! {
    #[derive(Copy, Clone)]
    #[repr(transparent)]
    struct PrDesc(u64);
    impl Debug;

    phys_addr, _: 51, 0;
    order, _: 62, 52;
    present, _: 63;
}

unsafe impl ByteValued for PrDesc {}

const FPR_TYPE_FREE: u32 = 0;
const FPR_TYPE_UNREPORT: u32 = 1;

pub(crate) const AVAIL_FEATURES: u64 = 1 << uapi::VIRTIO_F_VERSION_1 as u64
    | 1 << uapi::VIRTIO_BALLOON_F_STATS_VQ as u64
    | 1 << uapi::VIRTIO_BALLOON_F_FREE_PAGE_HINT as u64
    | 1 << uapi::VIRTIO_BALLOON_F_REPORTING as u64;

pub(crate) type BalloonSignal = SignalChannel<BalloonSignalMask, BalloonWakers>;

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

struct QueuedReport {
    req: OrbvmFprRequest,
    descs: Vec<PrDesc>,
}

pub struct Balloon {
    self_ref: Weak<Mutex<Self>>,
    pub(crate) signal: Arc<BalloonSignal>,
    pub(crate) queues: NumEnumMap<BalloonQueues, VirtQueue>,
    pub(crate) avail_features: u64,
    pub(crate) acked_features: u64,
    pub(crate) interrupt_status: Arc<AtomicUsize>,
    pub(crate) device_state: DeviceState,
    worker: Option<thread::JoinHandle<()>>,
    config: VirtioBalloonConfig,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,
    parker: Option<Arc<dyn Parkable>>,
    queued_reports: VecDeque<QueuedReport>,
}

impl Balloon {
    pub(crate) fn with_queues(queues: Vec<VirtQueue>) -> super::Result<Arc<Mutex<Balloon>>> {
        let config = VirtioBalloonConfig::default();

        Ok(Arc::new_cyclic(|self_ref| {
            Mutex::new(Balloon {
                self_ref: self_ref.clone(),
                signal: Arc::new(SignalChannel::new(BalloonWakers::default())),
                queues: NumEnumMap::from_iter(queues),
                avail_features: AVAIL_FEATURES,
                acked_features: 0,
                interrupt_status: Arc::new(AtomicUsize::new(0)),
                device_state: DeviceState::Inactive,
                worker: None,
                config,
                intc: None,
                irq_line: None,
                parker: None,
                queued_reports: VecDeque::new(),
            })
        }))
    }

    pub fn new() -> super::Result<Arc<Mutex<Balloon>>> {
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

    pub fn create_hvc_device(&self, _mem: GuestMemoryMmap) -> BalloonHvcDevice {
        BalloonHvcDevice::new(self.self_ref.upgrade().unwrap())
    }

    pub fn signal_used_queue(&self) {
        debug!("raising IRQ");

        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);

        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
        } else {
            self.signal.assert(BalloonSignalMask::INTERRUPT);
        }
    }

    pub fn process_frq(&mut self) -> bool {
        debug!("process_frq()");
        let mem = match self.device_state {
            DeviceState::Activated(ref mem) => mem,
            // This should never happen, it's been already validated in the event handler.
            DeviceState::Inactive => unreachable!(),
        };

        let mut have_used = false;

        // balloon guard is only needed here due to map/unmap
        hvf::set_balloon(true);
        let _guard = scopeguard::guard((), |_| hvf::set_balloon(false));

        while let Some(head) = self.queues[BalloonQueues::FRQ].pop(mem) {
            have_used = true;

            let index = head.index;

            // process the request
            if let Err(e) = self.process_one_fpr_virtio(mem, head) {
                error!("failed to process FRQ: {:?}", e);
            }

            // always consume the descriptor chain
            if let Err(e) = self.queues[BalloonQueues::FRQ].add_used(mem, index, 0) {
                error!("failed to add used elements to the queue: {:?}", e);
            }
        }

        have_used
    }

    fn process_one_fpr_virtio(
        &self,
        mem: &GuestMemoryMmap,
        head: DescriptorChain,
    ) -> anyhow::Result<()> {
        // first descriptor = request header
        let mut iter = head.into_iter();
        let req_desc = iter
            .next()
            .ok_or_else(|| anyhow!("no request header descriptor"))?;
        if req_desc.len as usize != size_of::<OrbvmFprRequest>() {
            return Err(anyhow!("invalid request header length"));
        }

        let req = mem.read_obj::<OrbvmFprRequest>(req_desc.addr)?;

        // second descriptor = prdesc buffer
        let prdescs_desc = iter
            .next()
            .ok_or_else(|| anyhow!("no prdesc buffer descriptor"))?;
        if req_desc.len % size_of::<PrDesc>() as u32 != 0 {
            return Err(anyhow!("invalid prdesc buffer length"));
        }

        // iterate through prdescs
        let slice = mem.get_slice(prdescs_desc.addr, prdescs_desc.len as usize)?;
        let slice_ptr = slice.ptr_guard();
        // turn it into a slice
        let prdescs = unsafe {
            std::slice::from_raw_parts(
                slice_ptr.as_ptr() as *const PrDesc,
                slice.len() / size_of::<PrDesc>(),
            )
        };

        // process the request
        self.process_one_fpr(mem, req, prdescs)
    }

    fn process_one_fpr(
        &self,
        mem: &GuestMemoryMmap,
        req: OrbvmFprRequest,
        prdescs: &[PrDesc],
    ) -> anyhow::Result<()> {
        let before = Instant::now();
        let mut total_bytes = 0;
        let mut num_ranges = 0;

        for prdesc in prdescs {
            // entry was invalidated in-place to avoid shifting array
            if !prdesc.present() {
                continue;
            }

            let guest_addr = GuestAddress(prdesc.phys_addr());
            let size = (req.guest_page_size << prdesc.order()) as usize;
            // bounds check
            let host_addr = mem.get_slice(guest_addr, size)?.ptr_guard();

            // free this range
            debug!(
                "should release guest_addr={:?} host_addr={:p} len={}",
                guest_addr,
                host_addr.as_ptr(),
                size
            );
            match req.type_ {
                FPR_TYPE_FREE => {
                    unsafe { hvf::free_range(guest_addr, host_addr.as_ptr() as *mut _, size)? };
                }
                FPR_TYPE_UNREPORT => {
                    unsafe { hvf::reuse_range(guest_addr, host_addr.as_ptr() as *mut _, size)? };
                }
                _ => {
                    error!("unknown free-page-report type");
                }
            }

            num_ranges += 1;
            total_bytes += size as u64;
        }

        info!(
            "[{}] ranges={:?} kib={}  time={:?}",
            if req.type_ == FPR_TYPE_FREE {
                "free"
            } else {
                "reuse"
            },
            num_ranges,
            total_bytes / 1024,
            before.elapsed()
        );

        Ok(())
    }

    fn process_queued_fprs(&mut self) {
        debug!("process_queued_fprs()");
        let mem = match self.device_state {
            DeviceState::Activated(ref mem) => mem,
            // This should never happen, it's been already validated in the event handler.
            DeviceState::Inactive => unreachable!(),
        };

        while let Some(qr) = self.queued_reports.pop_front() {
            if let Err(e) = self.process_one_fpr(mem, qr.req, &qr.descs) {
                error!("failed to process queued FPR: {:?}", e);
            }
        }
    }

    fn queue_fpr(&mut self, args_addr: GuestAddress) -> anyhow::Result<()> {
        let mem = match self.device_state {
            DeviceState::Activated(ref mem) => mem,
            DeviceState::Inactive => return Err(anyhow!("HVC call on inactive device")),
        };

        let req: OrbvmFprRequest = mem.read_obj(args_addr)?;

        // the purpose of async report is so that worker thread can process it without blocking, so copy the buffer
        let vs = mem.get_slice(
            GuestAddress(req.descs_addr),
            req.nr_descs as usize * size_of::<PrDesc>(),
        )?;
        let ptr = vs.ptr_guard();
        let slice = unsafe {
            std::slice::from_raw_parts(ptr.as_ptr() as *const PrDesc, req.nr_descs as usize)
        };

        // add to queue
        self.queued_reports.push_back(QueuedReport {
            req,
            descs: slice.to_vec(),
        });

        // assert signal
        self.signal.assert(BalloonSignalMask::REUSE);

        Ok(())
    }

    fn run_worker(me: Arc<Mutex<Self>>, signal: Arc<BalloonSignal>) {
        loop {
            signal.wait_on_park(BalloonSignalMask::all());

            let taken = signal.take(BalloonSignalMask::all());

            if taken.intersects(BalloonSignalMask::SHUTDOWN_WORKER) {
                break;
            }

            // process reuse requests first
            let mut me = me.lock().unwrap();
            if taken.intersects(BalloonSignalMask::REUSE) {
                debug!("async reuse event");
                me.process_queued_fprs();
            }

            if taken.intersects(BalloonSignalMask::IFQ) {
                error!("unsupported inflate queue event");
            }

            if taken.intersects(BalloonSignalMask::DFQ) {
                error!("unsupported deflate queue event");
            }

            if taken.intersects(BalloonSignalMask::STQ) {
                debug!("stats queue event (ignored)");
            }

            if taken.intersects(BalloonSignalMask::PHQ) {
                error!("unsupported page-hinting queue event");
            }

            if taken.intersects(BalloonSignalMask::FRQ) {
                debug!("free-page reporting queue event");

                if me.process_frq() {
                    me.signal_used_queue();
                }
            }
        }
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
        VirtioQueueSignals::new(
            self.signal.clone(),
            make_bit_flag_range!([
                BalloonSignalMask::IFQ,
                BalloonSignalMask::DFQ,
                BalloonSignalMask::STQ,
                BalloonSignalMask::PHQ,
                BalloonSignalMask::FRQ,
            ]),
        )
    }

    fn interrupt_signal(&self) -> BoundSignalChannelRef<'_> {
        BoundSignalChannelRef::new(&*self.signal, BalloonSignalMask::INTERRUPT)
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
            "guest driver attempted to write device config (offset={:x}, len={:x})",
            offset,
            data.len()
        );
    }

    fn activate(&mut self, mem: GuestMemoryMmap) -> ActivateResult {
        self.device_state = DeviceState::Activated(mem);

        if self.worker.is_none() {
            let me = self.self_ref.upgrade().unwrap();
            let signal = self.signal.clone();

            self.worker = Some(
                thread::Builder::new()
                    .name("balloon thread".to_string())
                    .spawn(move || Self::run_worker(me, signal))
                    .expect("failed to spawn balloon worker"),
            );
        }

        Ok(())
    }

    fn reset(&mut self) -> bool {
        if let Some(worker) = self.worker.take() {
            self.signal.assert(BalloonSignalMask::SHUTDOWN_WORKER);
            if let Err(err) = worker.join() {
                error!("Failed to shutdown balloon worker: {err:?}");
            }
        }

        true
    }

    fn is_activated(&self) -> bool {
        match self.device_state {
            DeviceState::Inactive => false,
            DeviceState::Activated(_) => true,
        }
    }
}

impl VmmExitObserver for Balloon {
    fn on_vmm_exit(&mut self) {
        self.reset();
    }
}

pub struct BalloonHvcDevice {
    balloon: Arc<Mutex<Balloon>>,
}

impl BalloonHvcDevice {
    fn new(balloon: Arc<Mutex<Balloon>>) -> Self {
        Self { balloon }
    }
}

impl HvcDevice for BalloonHvcDevice {
    fn call_hvc(&self, args_addr: GuestAddress) -> i64 {
        let mut balloon = self.balloon.lock().unwrap();
        match balloon.queue_fpr(args_addr) {
            Ok(()) => 0,
            Err(e) => {
                error!("failed to queue FPR: {:?}", e);
                -1
            }
        }
    }

    fn hvc_id(&self) -> Option<usize> {
        Some(HVC_DEVICE_BALLOON)
    }
}
