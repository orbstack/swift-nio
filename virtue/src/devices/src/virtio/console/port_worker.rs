use std::{io, sync::Arc};

use gruel::{DynamicMioChannelExt, DynamicMioWaker, SignalChannel};
use utils::{memory::GuestMemory, Mutex};

use crate::virtio::Queue;

use super::{
    device::{PortSignalMask, PortWakers},
    irq_signaler::IRQSignaler,
    port_io::{PortInput, PortOutput},
};

pub struct PortWorker {
    mem: GuestMemory,
    rx_queue: Queue,
    tx_queue: Queue,
    irq: IRQSignaler,
    signal: Arc<SignalChannel<PortSignalMask, PortWakers>>,
    input: Option<Arc<Mutex<Box<dyn PortInput + Send>>>>,
    output: Option<Arc<Mutex<Box<dyn PortOutput + Send>>>>,

    partial_tx_len: usize,
}

const WAKER_TOKEN: mio::Token = mio::Token(2);

impl PortWorker {
    pub fn new(
        mem: GuestMemory,
        rx_queue: Queue,
        tx_queue: Queue,
        irq: IRQSignaler,
        signal: Arc<SignalChannel<PortSignalMask, PortWakers>>,
        input: Option<Arc<Mutex<Box<dyn PortInput + Send>>>>,
        output: Option<Arc<Mutex<Box<dyn PortOutput + Send>>>>,
    ) -> Self {
        Self {
            mem,
            rx_queue,
            tx_queue,
            irq,
            signal,
            input,
            output,
            partial_tx_len: 0,
        }
    }

    pub fn run(&mut self) {
        trace!("port worker started");

        // Setup epoll once
        let mut poll = mio::Poll::new().unwrap();
        let mut events = mio::Events::with_capacity(32);

        let input_token = mio::Token(0);
        if let Some(input) = &self.input {
            if let Some(fd) = input.lock().unwrap().ready_fd() {
                poll.registry()
                    .register(
                        &mut mio::unix::SourceFd(&fd),
                        input_token,
                        mio::Interest::READABLE,
                    )
                    .unwrap();
            }
        }

        let output_token = mio::Token(1);
        if let Some(output) = &self.output {
            if let Some(fd) = output.lock().unwrap().ready_fd() {
                poll.registry()
                    .register(
                        &mut mio::unix::SourceFd(&fd),
                        output_token,
                        mio::Interest::WRITABLE,
                    )
                    .unwrap();
            }
        }

        let waker = mio::Waker::new(poll.registry(), WAKER_TOKEN).unwrap();
        self.signal
            .waker_state::<DynamicMioWaker>()
            .set_waker(Arc::new(waker));

        loop {
            if let Err(e) =
                self.signal
                    .wait_on_poll(PortSignalMask::all(), &mut poll, &mut events, None)
            {
                error!("Failed to wait on poll: {:?}", e);
                break;
            }

            let taken = self.signal.take(PortSignalMask::all());

            if taken.contains(PortSignalMask::STOP) {
                trace!("stop signal");
                return;
            }

            if taken.contains(PortSignalMask::RXQ) {
                trace!("rxq signal");
                if let Err(e) = self.process_rx() {
                    error!("Failed to process rx: {:?}", e);
                }
            }

            if taken.contains(PortSignalMask::TXQ) {
                trace!("txq signal");
                if let Err(e) = self.process_tx() {
                    error!("Failed to process tx: {:?}", e);
                }
            }

            for event in events.iter() {
                if event.token() == WAKER_TOKEN {
                    continue;
                }

                if event.is_read_closed() {
                    // unregister the fd. this is not edge-triggered
                    trace!("fd read closed");
                    if let Some(input) = &self.input {
                        if let Some(fd) = input.lock().unwrap().ready_fd() {
                            poll.registry()
                                .deregister(&mut mio::unix::SourceFd(&fd))
                                .unwrap();
                        }
                    }
                }

                if event.is_write_closed() {
                    // unregister the fd. this is not edge-triggered
                    trace!("fd write closed");
                    if let Some(output) = &self.output {
                        if let Some(fd) = output.lock().unwrap().ready_fd() {
                            poll.registry()
                                .deregister(&mut mio::unix::SourceFd(&fd))
                                .unwrap();
                        }
                    }
                }

                if event.is_readable() {
                    trace!("fd readable");
                    if let Err(e) = self.process_rx() {
                        error!("Failed to process rx: {:?}", e);
                    }
                }

                if event.is_writable() {
                    trace!("fd writable");
                    if let Err(e) = self.process_tx() {
                        error!("Failed to process tx: {:?}", e);
                    }
                }
            }
        }
    }

    fn process_rx(&mut self) -> anyhow::Result<()> {
        trace!("process_rx");

        let Some(input) = &self.input else {
            return Ok(());
        };
        let mut input = input.lock().unwrap();

        let mut has_used = false;
        loop {
            let Some(head) = self.rx_queue.pop(&self.mem) else {
                break;
            };

            let head_index = head.index;
            let mut bytes_read = 0;
            for desc in head.into_iter().writable() {
                let vs = self.mem.get_slice(desc.addr, desc.len as usize)?;
                match input.read_volatile(vs) {
                    Ok(0) => {
                        // EOF
                        break;
                    }
                    Ok(len) => {
                        bytes_read += len;
                    }
                    Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                        // no more data to read
                        break;
                    }
                    Err(e) => {
                        tracing::error!("Failed to read: {e:?}")
                    }
                }
            }

            if bytes_read != 0 {
                if let Err(e) = self
                    .rx_queue
                    .add_used(&self.mem, head_index, bytes_read as u32)
                {
                    error!("failed to add used elements to the queue: {:?}", e);
                }

                has_used = true;
            } else {
                self.rx_queue.undo_pop();
                // nothing more to read from fd
                break;
            }
        }

        if has_used {
            self.irq.signal_used_queue("rx processed");
        }

        Ok(())
    }

    fn process_tx(&mut self) -> anyhow::Result<()> {
        trace!("process_tx");

        let Some(output) = &self.output else {
            return Ok(());
        };
        let mut output = output.lock().unwrap();

        let mut has_used = false;
        loop {
            let Some(head) = self.tx_queue.pop(&self.mem) else {
                break;
            };

            let head_index = head.index;
            let mut bytes_written = 0;
            let mut skip_bytes = self.partial_tx_len;
            let mut total_desc_len = 0;
            for desc in head.into_iter().readable() {
                total_desc_len += desc.len as usize;
                let mut vs = self.mem.get_slice(desc.addr, desc.len as usize)?;

                // skip?
                if desc.len as usize <= skip_bytes {
                    skip_bytes -= desc.len as usize;
                    continue;
                } else if skip_bytes != 0 {
                    // partial write
                    vs = vs.try_get(skip_bytes..).unwrap_or(vs);
                    skip_bytes = 0;
                }

                trace!("writing {} bytes", vs.len());
                match output.write_volatile(vs) {
                    Ok(0) => {
                        // EOF
                        break;
                    }
                    Ok(len) => {
                        bytes_written += len;

                        if len != desc.len as usize {
                            // on partial write, keep this buffer in the queue, and retry when the file becomes writable
                            break;
                        }
                    }
                    Err(e) if e.kind() == io::ErrorKind::WouldBlock => {
                        // no more data to write
                        break;
                    }
                    Err(e) => {
                        tracing::error!("Failed to write: {e:?}");
                        break;
                    }
                }
            }

            if bytes_written == total_desc_len {
                if let Err(e) = self
                    .tx_queue
                    .add_used(&self.mem, head_index, bytes_written as u32)
                {
                    error!("failed to add used elements to the queue: {:?}", e);
                }

                // we finished a full buffer, whether it was an error or full write
                self.partial_tx_len = 0;
                has_used = true;
            } else {
                self.partial_tx_len = bytes_written;
                self.tx_queue.undo_pop();
                break;
            }
        }

        if has_used {
            self.irq.signal_used_queue("tx processed");
        }

        Ok(())
    }
}
