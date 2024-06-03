use std::{
    collections::HashMap,
    io,
    sync::{
        atomic::{AtomicBool, Ordering::*},
        Arc,
    },
};

use mio::event::Source;
use std::sync::Mutex;

use crate::{process_signals_multiplexed, ShutdownSignal, SignalMultiplexHandler};

// === MioDispatcher === //

pub struct MioDispatcher {
    poll: mio::Poll,
    events: mio::Events,
    abort_waker: Arc<mio::Waker>,
    ptr_to_handler: HashMap<usize, mio::Token>,
    handlers: Vec<Arc<Mutex<dyn MioSubscriber>>>,
}

pub trait MioSubscriber: 'static {
    fn process(&mut self);
}

impl MioDispatcher {
    const ABORT_TOKEN: mio::Token = mio::Token(usize::MAX);

    pub fn new() -> io::Result<Self> {
        let poll = mio::Poll::new()?;
        let events = mio::Events::with_capacity(1024);
        let abort_waker = mio::Waker::new(poll.registry(), Self::ABORT_TOKEN)?;

        Ok(Self {
            poll,
            events,
            abort_waker: Arc::new(abort_waker),
            ptr_to_handler: HashMap::default(),
            handlers: Vec::new(),
        })
    }

    pub fn abort_waker(&self) -> &Arc<mio::Waker> {
        &self.abort_waker
    }

    pub fn register(
        &mut self,
        source: &mut impl Source,
        interests: mio::Interest,
        subscriber: &Arc<Mutex<dyn MioSubscriber>>,
    ) -> io::Result<()> {
        let token = *self
            .ptr_to_handler
            .entry(Arc::as_ptr(subscriber) as *const () as usize)
            .or_insert_with(|| {
                let token = mio::Token(self.handlers.len());
                self.handlers.push(subscriber.clone());
                token
            });

        self.poll.registry().register(source, token, interests)
    }

    pub fn run(&mut self) -> io::Result<()> {
        self.poll.poll(&mut self.events, None)?;

        for event in self.events.iter() {
            self.handlers[event.token().0].lock().unwrap().process();
        }

        Ok(())
    }
}

// === Multiplexer Integration === //

pub fn process_signals_multiplexed_mio(
    shutdown: &ShutdownSignal,
    mio: &mut MioDispatcher,
    handlers: &mut [&mut dyn SignalMultiplexHandler],
) {
    let waker = mio.abort_waker().clone();
    let should_stop = Arc::new(AtomicBool::new(false));

    let _task = shutdown.spawn_ref({
        let waker = waker.clone();
        let should_stop = should_stop.clone();

        move || {
            should_stop.store(true, Relaxed);

            if let Err(err) = waker.wake() {
                tracing::error!("failed to send epoll waker signal to initiate shutdown: {err}");
            }
        }
    });

    process_signals_multiplexed(
        handlers,
        &should_stop,
        || {
            if let Err(err) = mio.run() {
                tracing::error!("failed to send process epoll signals: {err}");
                should_stop.store(true, Relaxed);
            }
        },
        || {
            if let Err(err) = waker.wake() {
                tracing::error!(
                    "failed to send epoll waker signal to process non-epoll signal: {err}"
                );
            }
        },
    );
}
