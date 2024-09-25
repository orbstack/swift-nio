// Copyright 2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Portions Copyright 2017 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the THIRD-PARTY file.

use std::os::{fd::RawFd, unix::io::AsRawFd};

use gruel::{InterestCtrl, Subscriber};
use mio::Interest;

use super::device::{Vsock, EVQ_INDEX, RXQ_INDEX, TXQ_INDEX};
use crate::virtio::VirtioDevice;

impl Vsock {
    pub(crate) fn handle_rxq_event(&mut self, event: &mio::event::Event) -> bool {
        debug!("vsock: RX queue event");

        if !event.is_readable() {
            warn!("vsock: rxq unexpected event {event:?}");
            return false;
        }

        let mut raise_irq = false;
        if let Err(e) = self.queue_events[RXQ_INDEX].read() {
            error!("Failed to get vsock rx queue event: {e:?}");
        } else {
            raise_irq |= self.process_stream_rx();
        }
        raise_irq
    }

    pub(crate) fn handle_txq_event(&mut self, event: &mio::event::Event) -> bool {
        debug!("vsock: TX queue event");

        if !event.is_readable() {
            warn!("vsock: txq unexpected event {event:?}");
            return false;
        }

        let mut raise_irq = false;
        if let Err(e) = self.queue_events[TXQ_INDEX].read() {
            error!("Failed to get vsock tx queue event: {:?}", e);
        } else {
            raise_irq |= self.process_stream_tx();
            // The backend may have queued up responses to the packets we sent during
            // TX queue processing. If that happened, we need to fetch those responses
            // and place them into RX buffers.
            if self.muxer.has_pending_rx() {
                raise_irq |= self.process_stream_rx();
            }
        }
        raise_irq
    }

    fn handle_evq_event(&mut self, event: &mio::event::Event) -> bool {
        debug!("vsock: event queue event");

        if !event.is_readable() {
            warn!("vsock: evq unexpected event {event:?}");
            return false;
        }

        if let Err(e) = self.queue_events[EVQ_INDEX].read() {
            error!("Failed to consume vsock evq event: {:?}", e);
        }

        true
    }

    fn handle_activate_event(&self, ctrl: &mut InterestCtrl<RawFd>) {
        debug!("vsock: activate event");

        if let Err(e) = self.activate_evt.read() {
            error!("Failed to consume vsock activate event: {:?}", e);
        }

        ctrl.register_fd(&self.queue_events[RXQ_INDEX], Interest::READABLE);
        ctrl.register_fd(&self.queue_events[TXQ_INDEX], Interest::READABLE);
    }
}

impl Subscriber for Vsock {
    type EventMeta = RawFd;

    fn process_event(
        &mut self,
        ctrl: &mut InterestCtrl<'_, RawFd>,
        event: &mio::event::Event,
        &mut source: &mut RawFd,
    ) {
        let rxq = self.queue_events[RXQ_INDEX].as_raw_fd();
        let txq = self.queue_events[TXQ_INDEX].as_raw_fd();
        let evq = self.queue_events[EVQ_INDEX].as_raw_fd();
        //let backend = self.backend.as_raw_fd();
        let activate_evt = self.activate_evt.as_raw_fd();

        if self.is_activated() {
            let mut raise_irq = false;
            match source {
                _ if source == rxq => raise_irq = self.handle_rxq_event(event),
                _ if source == txq => raise_irq = self.handle_txq_event(event),
                _ if source == evq => raise_irq = self.handle_evq_event(event),
                /*
                _ if source == backend => {
                    raise_irq = self.notify_backend(event);
                }
                */
                _ if source == activate_evt => {
                    self.handle_activate_event(ctrl);
                }
                _ => warn!("Unexpected vsock event received: {:?}", source),
            }
            if raise_irq {
                debug!("raising IRQ");
                self.signal_used_queue().unwrap_or_default();
            }
        } else {
            warn!(
                "Vsock: The device is not yet activated. Spurious event received: {:?}",
                source
            );
        }
    }

    fn init_interests(&self, ctrl: &mut InterestCtrl<'_, Self::EventMeta>) {
        ctrl.register_fd(&self.activate_evt, Interest::READABLE);
    }
}
