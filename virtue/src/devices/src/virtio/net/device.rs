// Copyright 2020 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Portions Copyright 2017 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the THIRD-PARTY file.
use crate::legacy::Gic;
use crate::virtio::net::{Error, Result};
use crate::virtio::net::{QUEUE_SIZES, RX_INDEX, TX_INDEX};
use crate::virtio::queue::Error as QueueError;
use crate::virtio::{ActivateResult, DeviceState, Queue, VirtioDevice, VmmExitObserver, TYPE_NET};
use crate::Error as DeviceError;

use super::backend::{NetBackend, ReadError, WriteError};
use super::callback::{CallbackBackend, HostNetCallbacks};
use super::dgram::Dgram;
use super::worker::NetWorker;

use bitflags::bitflags;
use bytemuck::{Pod, Zeroable};
use gruel::{
    define_waker_set, ArcBoundSignalChannel, BoundSignalChannel, OnceMioWaker, ParkWaker,
    SignalChannel,
};
use std::cmp;
use std::io::Write;
use std::os::fd::OwnedFd;
use std::sync::Arc;
use std::thread::JoinHandle;
use utils::eventfd::{EventFd, EFD_NONBLOCK};
use utils::memory::{GuestMemory, InvalidGuestAddress};
use virtio_bindings::virtio_net::{
    VIRTIO_NET_F_CSUM, VIRTIO_NET_F_GUEST_CSUM, VIRTIO_NET_F_GUEST_TSO4, VIRTIO_NET_F_GUEST_TSO6,
    VIRTIO_NET_F_HOST_TSO4, VIRTIO_NET_F_HOST_TSO6, VIRTIO_NET_F_MAC, VIRTIO_NET_F_MTU,
};
use virtio_bindings::virtio_ring::VIRTIO_RING_F_EVENT_IDX;

const VIRTIO_F_VERSION_1: u32 = 32;

define_waker_set! {
    #[derive(Default)]
    pub(crate) struct NetWakers {
        park: ParkWaker,
        epoll: OnceMioWaker,
    }
}

bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub(crate) struct NetSignalMask: u64 {
        const SHUTDOWN_WORKER = 1 << 0;
        const GUEST_RXQ = 1 << 1;
        const GUEST_TXQ = 1 << 2;
    }
}

pub(crate) type NetSignalChannel = SignalChannel<NetSignalMask, NetWakers>;

#[derive(Debug)]
pub enum FrontendError {
    DescriptorChainTooSmall,
    EmptyQueue,
    GuestMemory(InvalidGuestAddress),
    QueueError(QueueError),
    ReadOnlyDescriptor,
    Backend(ReadError),
}

#[derive(Debug)]
pub enum RxError {
    Frontend(FrontendError),
    DeviceError(DeviceError),
}

#[derive(Debug)]
pub enum TxError {
    Backend(WriteError),
    DeviceError(DeviceError),
    QueueError(QueueError),
}

#[derive(Debug, Copy, Clone, Default, Pod, Zeroable)]
#[repr(C, packed)]
struct VirtioNetConfig {
    mac: [u8; 6],
    status: u16,
    max_virtqueue_pairs: u16,
    mtu: u16,
}

#[derive(Clone)]
pub enum VirtioNetBackend {
    Dgram(Arc<OwnedFd>),
    Callback(Arc<dyn HostNetCallbacks>),
}

impl VirtioNetBackend {
    pub(crate) fn create(
        self,
        queues: &[Queue],
        mem: &GuestMemory,
        intc: &Option<Arc<Gic>>,
        irq_line: &Option<u32>,
    ) -> Box<dyn NetBackend + Send> {
        match self {
            VirtioNetBackend::Dgram(fd) => {
                Box::new(Dgram::new(fd).unwrap()) as Box<dyn NetBackend + Send>
            }

            VirtioNetBackend::Callback(callbacks) => Box::new(CallbackBackend::new(
                callbacks,
                queues[RX_INDEX].clone(),
                mem.clone(),
                intc.clone(),
                *irq_line,
            )),
        }
    }
}

pub struct Net {
    id: String,
    cfg_backend: VirtioNetBackend,

    avail_features: u64,
    acked_features: u64,

    queues: Vec<Queue>,
    signals: Arc<NetSignalChannel>,

    pub(crate) device_state: DeviceState,

    intc: Option<Arc<Gic>>,
    irq_line: Option<u32>,

    config: VirtioNetConfig,

    worker_thread: Option<JoinHandle<()>>,
}

impl Net {
    /// Create a new virtio network device using the backend
    pub fn new(id: String, cfg_backend: VirtioNetBackend, mac: [u8; 6], mtu: u16) -> Result<Self> {
        let avail_features = (1 << VIRTIO_NET_F_GUEST_CSUM) | (1 << VIRTIO_NET_F_CSUM) | (1 << VIRTIO_NET_F_GUEST_TSO4) | (1 << VIRTIO_NET_F_HOST_TSO4) | (1 << VIRTIO_NET_F_GUEST_TSO6) | (1 << VIRTIO_NET_F_HOST_TSO6) | (1 << VIRTIO_NET_F_MAC) | (1 << VIRTIO_NET_F_MTU) | (1 << VIRTIO_RING_F_EVENT_IDX)
            | 1 << VIRTIO_F_VERSION_1;

        let mut queue_evts = Vec::new();
        for _ in QUEUE_SIZES.iter() {
            queue_evts.push(EventFd::new(EFD_NONBLOCK).map_err(Error::EventFd)?);
        }

        let queues = QUEUE_SIZES.iter().map(|&s| Queue::new(s)).collect();

        let config = VirtioNetConfig {
            mac,
            status: 0,
            max_virtqueue_pairs: 0,
            mtu,
        };

        Ok(Net {
            id,
            cfg_backend,

            avail_features,
            acked_features: 0u64,

            queues,
            signals: Arc::new(SignalChannel::new(NetWakers::default())),

            device_state: DeviceState::Inactive,

            intc: None,
            irq_line: None,

            config,
            worker_thread: None,
        })
    }

    /// Provides the ID of this net device.
    pub fn id(&self) -> &str {
        &self.id
    }

    pub fn set_intc(&mut self, intc: Arc<Gic>) {
        self.intc = Some(intc);
    }
}

impl VirtioDevice for Net {
    fn avail_features(&self) -> u64 {
        self.avail_features
    }

    fn acked_features(&self) -> u64 {
        self.acked_features
    }

    fn set_acked_features(&mut self, acked_features: u64) {
        self.acked_features = acked_features;
    }

    fn device_type(&self) -> u32 {
        TYPE_NET
    }

    fn queues(&self) -> &[Queue] {
        &self.queues
    }

    fn queues_mut(&mut self) -> &mut [Queue] {
        &mut self.queues
    }

    fn queue_signals(&self) -> Vec<ArcBoundSignalChannel> {
        vec![
            BoundSignalChannel::new(self.signals.clone(), NetSignalMask::GUEST_RXQ),
            BoundSignalChannel::new(self.signals.clone(), NetSignalMask::GUEST_TXQ),
        ]
    }

    fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(irq);
    }

    fn read_config(&self, offset: u64, mut data: &mut [u8]) {
        let config_slice = bytemuck::bytes_of(&self.config);
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
        tracing::warn!(
            "Net: guest driver attempted to write device config (offset={:x}, len={:x})",
            offset,
            data.len()
        );
    }

    fn activate(&mut self, mem: GuestMemory) -> ActivateResult {
        let event_idx: bool = (self.acked_features & (1 << VIRTIO_RING_F_EVENT_IDX)) != 0;
        self.queues[RX_INDEX].set_event_idx(event_idx);
        self.queues[TX_INDEX].set_event_idx(event_idx);

        let worker = NetWorker::new(
            self.signals.clone(),
            self.queues.clone(),
            self.intc.clone(),
            self.irq_line,
            mem.clone(),
            self.cfg_backend.clone(),
            self.config.mtu,
        );
        self.worker_thread = Some(worker.run());

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

impl VmmExitObserver for Net {
    fn on_vmm_exit(&mut self) {
        debug!("Shutting down net");
        self.signals.assert(NetSignalMask::SHUTDOWN_WORKER);

        if let Some(thread) = self.worker_thread.take() {
            debug!("Joining on net");
            let _ = thread.join();
        }

        debug!("Done shutting down net");
    }
}
