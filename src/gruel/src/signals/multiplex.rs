use std::{
    io,
    sync::{atomic::AtomicBool, Arc},
};

use memmage::CloneDynRef;
use smallbox::SmallBox;

use crate::{DynamicallyBoundWaker, RawSignalChannel, ShutdownSignal};

#[cfg(not(loom))]
use std::sync::atomic::{AtomicU64, Ordering::*};

#[cfg(loom)]
use loom::sync::atomic::{AtomicU64, Ordering::*};

// === Core === //

pub trait SignalMultiplexHandler<C: ?Sized = ()> {
    fn process(&mut self, cx: &mut C);

    fn signals(&self, cx: &mut C) -> Vec<CloneDynRef<'static, RawSignalChannel>>;

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<Self>()
    }
}

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

pub fn multiplex_signals_with_shutdown<C: ?Sized>(
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

    multiplex_signals(handlers, &should_stop, parker);
}

pub fn multiplex_signals<C: ?Sized>(
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
    let parker_facade = MultiplexParker::subscriber_facade(&mut parker);
    let handler_signals = handlers
        .iter()
        .map(|handler| handler.signals(parker_facade))
        .collect::<Box<[_]>>();

    // Bind the subscriber to every handler.

    // We have to be *very* careful to only borrow things that only expire after all the wait guards
    // are gone. We also have to be careful to ensure that the `wait_guards` are dropped before the
    // `wakers` are dropped.
    let mut wakers = (0..handlers.len())
        .map(|i| {
            let slot_idx = i / 64;
            let slot_mask = 1 << (i % 64);
            let slot = &dirty_flags[slot_idx];

            move || {
                slot.fetch_or(slot_mask, Relaxed);
                (unpark)();
            }
        })
        .collect::<Box<_>>();

    let mut wait_guards = Vec::new();

    for (i, waker) in wakers.iter_mut().enumerate() {
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
                state.bind_waker(waker);
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

// === EventManager === //

pub struct EventManager {
    poll: mio::Poll,
    events: mio::Events,
    abort_waker: Arc<mio::Waker>,
    mio_handlers: Vec<usize>,
    subscribers: Vec<SmallBox<dyn Subscriber, *const ()>>,
}

impl EventManager {
    const ABORT_TOKEN: mio::Token = mio::Token(usize::MAX);

    pub fn new() -> io::Result<Self> {
        let poll = mio::Poll::new()?;
        let events = mio::Events::with_capacity(16);
        let abort_waker = mio::Waker::new(poll.registry(), Self::ABORT_TOKEN)?;

        Ok(Self {
            poll,
            events,
            abort_waker: Arc::new(abort_waker),
            mio_handlers: Vec::new(),
            subscribers: Vec::new(),
        })
    }

    pub fn abort_waker(&self) -> &Arc<mio::Waker> {
        &self.abort_waker
    }

    pub fn register(&mut self, handler: impl Subscriber) {
        self.subscribers.push(smallbox::smallbox!(handler));
    }

    pub fn run(&mut self, shutdown: &ShutdownSignal) {
        struct DispatchParker<'a>(&'a mut EventManager);

        impl MultiplexParker for DispatchParker<'_> {
            type SubscriberFacade = Self;

            fn subscriber_facade(me: &mut Self) -> &mut Self::SubscriberFacade {
                me
            }

            fn park(me: &mut Self, should_stop: &AtomicBool) {
                // Poll for events
                if let Err(err) = me.0.poll.poll(&mut me.0.events, None) {
                    tracing::error!("failed to send process epoll signals: {err}");
                    should_stop.store(true, Relaxed);
                    return;
                }

                // Process events
                for event in me.0.events.iter() {
                    if event.token().0 == usize::MAX {
                        continue;
                    }

                    let subscriber_idx = me.0.mio_handlers[event.token().0];
                    let subscriber = &mut me.0.subscribers[subscriber_idx];
                    let subscriber_name = subscriber.debug_type_name();

                    subscriber.process_event(
                        &mut InterestCtrl {
                            poll: &mut me.0.poll,
                            mio_handlers: &mut me.0.mio_handlers,
                            subscriber_name,
                            subscriber_idx,
                        },
                        event,
                    );
                }
            }

            fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static {
                let waker = me.0.abort_waker.clone();

                move || {
                    if let Err(err) = waker.wake() {
                        tracing::error!(
                            "failed to send epoll waker signal to process non-epoll signal: {err}"
                        );
                    }
                }
            }
        }

        struct SubscriberAdapter(usize, &'static str);

        impl<'a> SignalMultiplexHandler<DispatchParker<'a>> for SubscriberAdapter {
            fn process(&mut self, cx: &mut DispatchParker<'a>) {
                let event_mgr = &mut *cx.0;
                let subscriber_idx = self.0;
                let subscriber = &mut event_mgr.subscribers[subscriber_idx];
                let subscriber_name = subscriber.debug_type_name();

                subscriber.process_signals(&mut InterestCtrl {
                    poll: &mut event_mgr.poll,
                    mio_handlers: &mut event_mgr.mio_handlers,
                    subscriber_idx,
                    subscriber_name,
                });
            }

            fn signals(
                &self,
                cx: &mut DispatchParker<'a>,
            ) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
                cx.0.subscribers[self.0].signals()
            }

            fn debug_type_name(&self) -> &'static str {
                self.1
            }
        }

        let mut subscriber_list = self
            .subscribers
            .iter()
            .enumerate()
            .map(|(i, subscriber)| SubscriberAdapter(i, subscriber.debug_type_name()))
            .collect::<Box<_>>();

        let mut subscriber_ref_list = subscriber_list
            .iter_mut()
            .map(|v| v as &mut dyn SignalMultiplexHandler<DispatchParker<'_>>)
            .collect::<Box<_>>();

        multiplex_signals_with_shutdown(shutdown, &mut subscriber_ref_list, DispatchParker(self));
    }
}

pub struct InterestCtrl<'a> {
    poll: &'a mut mio::Poll,
    mio_handlers: &'a mut Vec<usize>,
    subscriber_idx: usize,
    subscriber_name: &'static str,
}

impl InterestCtrl<'_> {
    pub fn register(&mut self, source: &mut impl mio::event::Source, interests: mio::Interest) {
        let token = mio::Token(self.mio_handlers.len());

        if let Err(err) = self.poll.registry().register(source, token, interests) {
            tracing::error!(
                "`{}` failed to subscribe to an epoll target: {err}",
                self.subscriber_name
            );
        }

        self.mio_handlers.push(self.subscriber_idx);
    }
}

pub trait Subscriber: 'static + Send + Sync {
    fn process_signals(&mut self, ctl: &mut InterestCtrl<'_>);

    fn process_event(&mut self, ctl: &mut InterestCtrl<'_>, event: &mio::event::Event) {
        let _ = ctl;
        let _ = event;
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>>;

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<Self>()
    }
}
