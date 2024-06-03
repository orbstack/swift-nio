use std::{
    collections::HashMap,
    io,
    sync::{atomic::AtomicBool, Arc, Mutex},
};

use memmage::CloneDynRef;

use crate::{
    util::{dangling_unit, Parker},
    DynamicallyBoundWaker, RawSignalChannel, ShutdownSignal,
};

#[cfg(not(loom))]
use std::sync::atomic::{AtomicU64, Ordering::*};

#[cfg(loom)]
use loom::sync::atomic::{AtomicU64, Ordering::*};

// === SignalMultiplexHandler === //

pub trait SignalMultiplexHandler<C: ?Sized = ()> {
    fn process(&mut self, cx: &mut C);

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>>;

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<Self>()
    }
}

impl<C, T> SignalMultiplexHandler<C> for Arc<std::sync::Mutex<T>>
where
    C: ?Sized,
    T: ?Sized + SignalMultiplexHandler<C>,
{
    fn process(&mut self, cx: &mut C) {
        self.lock().unwrap().process(cx);
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.lock().unwrap().signals()
    }
}

impl<C, T> SignalMultiplexHandler<C> for Arc<std::sync::RwLock<T>>
where
    C: ?Sized,
    T: ?Sized + SignalMultiplexHandler<C>,
{
    fn process(&mut self, cx: &mut C) {
        self.write().unwrap().process(cx);
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.read().unwrap().signals()
    }
}

impl<C, T> SignalMultiplexHandler<C> for std::rc::Rc<std::cell::RefCell<T>>
where
    C: ?Sized,
    T: ?Sized + SignalMultiplexHandler<C>,
{
    fn process(&mut self, cx: &mut C) {
        self.borrow_mut().process(cx);
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.borrow().signals()
    }
}

// === MultiplexParker === //

pub trait MultiplexParker: Sized {
    type SubscriberFacade: ?Sized;

    fn subscriber_facade(me: &mut Self) -> &mut Self::SubscriberFacade;

    fn park(me: &mut Self, should_stop: &AtomicBool);

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static;
}

impl<T: ?Sized + MultiplexParker> MultiplexParker for &mut T {
    type SubscriberFacade = T::SubscriberFacade;

    fn subscriber_facade(me: &mut Self) -> &mut Self::SubscriberFacade {
        T::subscriber_facade(me)
    }

    fn park(me: &mut Self, should_stop: &AtomicBool) {
        T::park(me, should_stop)
    }

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static {
        T::unparker(*me)
    }
}

#[derive(Debug)]
pub struct ClosureMultiplexParker<FPark, FUnpark> {
    park: FPark,
    unpark: Arc<FUnpark>,
}

impl<FPark, FUnpark> ClosureMultiplexParker<FPark, FUnpark>
where
    FPark: FnMut(),
    FUnpark: Fn(),
{
    pub fn new(park: FPark, unpark: FUnpark) -> Self {
        Self {
            park,
            unpark: Arc::new(unpark),
        }
    }
}

impl<FPark, FUnpark> MultiplexParker for ClosureMultiplexParker<FPark, FUnpark>
where
    FPark: FnMut(),
    FUnpark: Fn() + Send + Sync + 'static,
{
    type SubscriberFacade = ();

    fn subscriber_facade(_me: &mut Self) -> &mut Self::SubscriberFacade {
        dangling_unit()
    }

    fn park(me: &mut Self, _should_stop: &AtomicBool) {
        (me.park)();
    }

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static {
        let unpark = me.unpark.clone();
        move || unpark()
    }
}

#[derive(Debug, Default)]
pub struct ParkMultiplexParker(Arc<Parker>);

impl MultiplexParker for ParkMultiplexParker {
    type SubscriberFacade = ();

    fn subscriber_facade(_me: &mut Self) -> &mut Self::SubscriberFacade {
        dangling_unit()
    }

    fn park(me: &mut Self, _should_stop: &AtomicBool) {
        me.0.park();
    }

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static {
        let parker = me.0.clone();
        move || parker.unpark()
    }
}

// === Core === //

pub fn process_signals_multiplexed_shutdown<C: ?Sized>(
    shutdown: &ShutdownSignal,
    handlers: &mut [&mut dyn SignalMultiplexHandler<C>],
    mut parker: impl MultiplexParker<SubscriberFacade = C>,
) {
    let should_stop = Arc::new(AtomicBool::new(false));
    let unparker = MultiplexParker::unparker(&mut parker);

    let _task = shutdown.spawn_ref({
        let should_stop = should_stop.clone();

        move || {
            should_stop.store(true, Relaxed);
            unparker();
        }
    });

    process_signals_multiplexed(handlers, &should_stop, parker);
}

pub fn process_signals_multiplexed<C: ?Sized>(
    handlers: &mut [&mut dyn SignalMultiplexHandler<C>],
    should_stop: &AtomicBool,
    mut parker: impl MultiplexParker<SubscriberFacade = C>,
) {
    // Create a bitflag for quickly determining which handlers are dirty.
    let dirty_flags = (0..(handlers.len() + 63) / 64)
        .map(|_| AtomicU64::new(0))
        .collect::<Box<[_]>>();

    let dirty_flags = &*dirty_flags;

    // Create a parker for this subscriber loop.
    let unpark = MultiplexParker::unparker(&mut parker);
    let unpark = &unpark;

    // Get handler signals before binding them so we can hold onto them after the binding loop.
    let handler_signals = handlers
        .iter()
        .map(|handler| handler.signals())
        .collect::<Box<[_]>>();

    // Bind the subscriber to every handler.
    let mut wait_guards = Vec::new();

    for (i, _handler) in handlers.iter().enumerate() {
        // Determine the bit in the dirty mask that this handler occupies.
        let slot_idx = i / 64;
        let slot_mask = 1 << (i % 64);
        let slot = &dirty_flags[slot_idx];

        // Bind each signal to the slot.
        for signal in &handler_signals[i] {
            let (state, waiter_idx) = signal
                .opt_waker::<DynamicallyBoundWaker>()
                .expect("only signals with a `DynamicallyBoundWaker` can be multiplexed");

            // Set the waker's dynamic waking closure. We could theoretically do better than a
            // `DynamicallyBoundWaker` with some clever reference-counting but I don't really want
            // to implement such a complex system for such a performance-insensitive system.
            unsafe {
                // We have to be *very* careful to only borrow things that only expire after all the
                // wait guards are gone.
                state.bind_waker(move || {
                    slot.fetch_or(slot_mask, Relaxed);
                    (unpark)();
                });
            }

            let wait_result = signal.wait_manual(u64::MAX, 0, waiter_idx);
            if wait_result.observed_mask != 0 {
                slot.fetch_or(slot_mask, Relaxed);
            }

            let undo_waker_guard = wait_result.wait_guard;
            let unbind_dynamic_guard = scopeguard::guard((), |()| {
                state.clear_waker();
            });
            wait_guards.push((undo_waker_guard, unbind_dynamic_guard));
        }
    }

    // Process events
    while !should_stop.load(Relaxed) {
        let parker_facade = MultiplexParker::subscriber_facade(&mut parker);

        for (i_cell, flag) in dirty_flags.iter().enumerate() {
            let mut flag = flag.swap(0, Relaxed);
            while flag != 0 {
                let i_bit = flag.trailing_zeros() as usize;
                flag ^= 1 << i_bit;
                let i = i_cell * 64 + i_bit;

                tracing::info_span!("process multiplexed signals").in_scope(|| {
                    tracing::trace!(
                        "Processing signals from `{}`",
                        handlers[i].debug_type_name(),
                    );
                    handlers[i].process(parker_facade);
                });

                #[cfg(debug_assertions)]
                for (signal_idx, signal) in handler_signals[i_cell].iter().enumerate() {
                    if signal.could_take(u64::MAX) {
                        tracing::warn!(
                            "Signal multiplex handler of type `{}` ignored some events from its \
                             subscribed signal at index {signal_idx}; this could cause unexpected \
                             behavior.",
                            handlers[i].debug_type_name(),
                        );
                    }
                }
            }
        }

        // Although we don't redo another `wait` operation here, this routine is still guaranteed not
        // to miss any events because `park` holds unpark tickets.
        MultiplexParker::park(&mut parker, should_stop);
    }
}

// === MioDispatcher === //

pub struct MioDispatcher {
    poll: mio::Poll,
    events: mio::Events,
    abort_waker: Arc<mio::Waker>,
    ptr_to_handler: HashMap<usize, mio::Token>,
    handlers: Vec<Arc<Mutex<dyn MioMultiplexHandler>>>,
}

pub trait MioMultiplexHandler: 'static {
    fn process(&mut self);
}

impl MioDispatcher {
    const ABORT_TOKEN: mio::Token = mio::Token(usize::MAX);

    pub fn new() -> io::Result<Self> {
        let poll = mio::Poll::new()?;
        let events = mio::Events::with_capacity(16);
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
        source: &mut impl mio::event::Source,
        interests: mio::Interest,
        subscriber: &Arc<Mutex<dyn MioMultiplexHandler>>,
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

impl MultiplexParker for MioDispatcher {
    type SubscriberFacade = Self;

    fn subscriber_facade(me: &mut Self) -> &mut Self::SubscriberFacade {
        me
    }

    fn park(me: &mut Self, should_stop: &AtomicBool) {
        should_stop.store(true, Relaxed);

        if let Err(err) = me.run() {
            tracing::error!("failed to send process epoll signals: {err}");
            should_stop.store(true, Relaxed);
        }
    }

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static {
        let waker = me.abort_waker.clone();

        move || {
            if let Err(err) = waker.wake() {
                tracing::error!(
                    "failed to send epoll waker signal to process non-epoll signal: {err}"
                );
            }
        }
    }
}
