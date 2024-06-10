use std::{
    any::Any,
    io,
    marker::PhantomData,
    os::fd::{AsRawFd, RawFd},
    sync::{atomic::AtomicBool, Arc},
};

use memmage::CloneDynRef;
use mio::unix::SourceFd;
use smallbox::SmallBox;

use crate::{DynamicallyBoundWaker, RawSignalChannel, ShutdownSignal};

#[cfg(not(loom))]
use std::sync::atomic::{AtomicU64, Ordering::*};

#[cfg(loom)]
use loom::sync::atomic::{AtomicU64, Ordering::*};

// === Core === //

pub trait SignalMultiplexHandler<C: ?Sized = ()> {
    fn process(&mut self, should_stop: &AtomicBool, cx: &mut C);

    fn signals(
        &self,
        should_stop: &AtomicBool,
        cx: &mut C,
    ) -> Vec<CloneDynRef<'static, RawSignalChannel>>;

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<Self>()
    }
}

pub trait MultiplexParker: Sized {
    type SubscriberCx: ?Sized;

    fn subscriber_cx(me: &mut Self) -> &mut Self::SubscriberCx;

    fn park(me: &mut Self, should_stop: &AtomicBool);

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static;
}

impl<T: ?Sized + MultiplexParker> MultiplexParker for &mut T {
    type SubscriberCx = T::SubscriberCx;

    fn subscriber_cx(me: &mut Self) -> &mut Self::SubscriberCx {
        T::subscriber_cx(me)
    }

    fn park(me: &mut Self, should_stop: &AtomicBool) {
        T::park(me, should_stop)
    }

    fn unparker(me: &mut Self) -> impl Fn() + Send + Sync + 'static {
        T::unparker(*me)
    }
}

pub fn multiplex_signals<C: ?Sized>(
    should_stop: &AtomicBool,
    handlers: &mut [&mut dyn SignalMultiplexHandler<C>],
    mut parker: impl MultiplexParker<SubscriberCx = C>,
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
    let parker_facade = MultiplexParker::subscriber_cx(&mut parker);
    let handler_signals = handlers
        .iter()
        .map(|handler| handler.signals(should_stop, parker_facade))
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
        let parker_facade = MultiplexParker::subscriber_cx(&mut parker);

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
                    handlers[i].process(should_stop, parker_facade);
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

pub fn multiplex_signals_with_shutdown<C: ?Sized>(
    shutdown: &ShutdownSignal,
    handlers: &mut [&mut dyn SignalMultiplexHandler<C>],
    mut parker: impl MultiplexParker<SubscriberCx = C>,
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

    multiplex_signals(&should_stop, handlers, parker);
}

// === EventManager === //

// Subscriber
pub trait Subscriber: 'static {
    type EventMeta: ?Sized + Send + Sync;

    fn process_signals(&mut self, ctrl: &mut InterestCtrl<'_, Self::EventMeta>) {
        let _ = ctrl;
    }

    fn process_event(
        &mut self,
        ctrl: &mut InterestCtrl<'_, Self::EventMeta>,
        event: &mio::event::Event,
        meta: &mut Self::EventMeta,
    ) {
        let _ = ctrl;
        let _ = event;
        let _ = meta;
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        Vec::new()
    }

    fn init_interests(&self, ctrl: &mut InterestCtrl<'_, Self::EventMeta>) {
        let _ = ctrl;
    }

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<Self>()
    }
}

// EventManager
pub struct EventManager {
    // The epoll registry
    poll: mio::Poll,

    // The epoll event queue
    events: mio::Events,

    // The unique waker used to unpark the event manager when new *signals* are received.
    abort_waker: Arc<mio::Waker>,

    // The set of user-defined `Subscriber`s. A given subscriber is typically only registered in
    // this list once. We use a SmallBox under the assumption that most of these objects are smart
    // pointers such as `Arc<Mutex<MySubscriber>>`.
    subscribers: Vec<SmallBox<ErasedSubscriber, *const ErasedSubscriber>>,

    // A set mapping SourceFd `Token`s to handlers, which describe the index of the subscriber to
    // which we should deliver the event and the metadata which should be used to deliver it.
    mio_handlers: Vec<MioHandler>,
}

type ErasedSubscriber = dyn Subscriber<EventMeta = dyn Any + Send + Sync> + Send + Sync;

struct MioHandler {
    subscriber_idx: usize,

    // This value is `None` while we pass `metadata` to a subscriber. This will always be returned
    // to the `MioHandler` instance after the event has been processed.
    metadata: Option<smallbox::SmallBox<dyn Any + Send + Sync, u64>>,
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

    pub fn register<S>(&mut self, handler: S)
    where
        S: Subscriber + Send + Sync,
        S::EventMeta: Sized,
    {
        // An adapter to erase the metadata type of the subscriber.
        struct SubscriberEraseAdapter<T>(T);

        impl<S> Subscriber for SubscriberEraseAdapter<S>
        where
            S: Subscriber + Send + Sync,
            S::EventMeta: Sized,
        {
            type EventMeta = dyn Any + Send + Sync;

            fn process_signals(&mut self, ctrl: &mut InterestCtrl<'_, Self::EventMeta>) {
                self.0.process_signals(ctrl.cast_meta());
            }

            fn process_event(
                &mut self,
                ctrl: &mut InterestCtrl<'_, Self::EventMeta>,
                event: &mio::event::Event,
                meta: &mut Self::EventMeta,
            ) {
                self.0.process_event(
                    ctrl.cast_meta(),
                    event,
                    meta.downcast_mut::<S::EventMeta>().unwrap(),
                );
            }

            fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
                self.0.signals()
            }

            fn init_interests(&self, ctrl: &mut InterestCtrl<'_, Self::EventMeta>) {
                self.0.init_interests(ctrl.cast_meta())
            }

            fn debug_type_name(&self) -> &'static str {
                self.0.debug_type_name()
            }
        }

        // Register the subscriber
        let subscriber_idx = self.subscribers.len();
        let subscriber_name = handler.debug_type_name();

        self.subscribers
            .push(smallbox::smallbox!(SubscriberEraseAdapter(handler)));

        // Allow the subscriber to register its initial interests. We ignore exit signals here since
        // expressing the unavailability of that operation properly would probably just hurt
        // ergonomics.
        self.subscribers[subscriber_idx].init_interests(&mut InterestCtrl {
            _ty: PhantomData,
            raw: InterestCtrlInner {
                poll: &mut self.poll,
                mio_handlers: &mut self.mio_handlers,
                subscriber_idx,
                subscriber_name,
                should_stop: &AtomicBool::new(true),
            },
        });
    }

    pub fn run(&mut self) {
        // Signal multiple parking handler; implements park by polling for new events and also handles
        // dispatching those events before yielding control back to the signal multiplexer.
        struct EventManagerParker<'a>(&'a mut EventManager);

        impl MultiplexParker for EventManagerParker<'_> {
            type SubscriberCx = EventManager;

            fn subscriber_cx(me: &mut Self) -> &mut EventManager {
                me.0
            }

            fn park(me: &mut Self, should_stop: &AtomicBool) {
                let me = &mut me.0;

                // Poll for events
                if let Err(err) = me.poll.poll(&mut me.events, None) {
                    tracing::error!("failed to send process epoll signals: {err}");
                    should_stop.store(true, Relaxed);
                    return;
                }

                // Process events
                for event in me.events.iter() {
                    // Ignore the waker event.
                    if event.token() == EventManager::ABORT_TOKEN {
                        continue;
                    }

                    // Figure out which subscriber we're waking up.
                    let handler = &mut me.mio_handlers[event.token().0];
                    let subscriber_idx = handler.subscriber_idx;
                    let subscriber = &mut me.subscribers[subscriber_idx];
                    let subscriber_name = subscriber.debug_type_name();

                    // Temporarily take the metadata so we can pass `mio_handlers` to `InterestCtrl`.
                    let metadata = handler.metadata.take().unwrap();
                    let mut mio_handlers_and_metadata = scopeguard::guard(
                        (&mut me.mio_handlers, metadata),
                        |(mio_handlers, metadata)| {
                            mio_handlers[event.token().0].metadata = Some(metadata);
                        },
                    );
                    let (mio_handlers, metadata) = &mut *mio_handlers_and_metadata;

                    subscriber.process_event(
                        &mut InterestCtrl {
                            _ty: PhantomData,
                            raw: InterestCtrlInner {
                                poll: &mut me.poll,
                                mio_handlers,
                                subscriber_idx,
                                subscriber_name,
                                should_stop,
                            },
                        },
                        event,
                        &mut **metadata,
                    );

                    // (`mio_handlers_and_metadata` dropped here before the loop runs again)
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

        struct EventManagerSubscriber(usize, &'static str);

        impl SignalMultiplexHandler<EventManager> for EventManagerSubscriber {
            fn process(&mut self, should_stop: &AtomicBool, event_mgr: &mut EventManager) {
                let subscriber_idx = self.0;
                let subscriber = &mut event_mgr.subscribers[subscriber_idx];
                let subscriber_name = subscriber.debug_type_name();

                subscriber.process_signals(&mut InterestCtrl {
                    _ty: PhantomData,
                    raw: InterestCtrlInner {
                        poll: &mut event_mgr.poll,
                        mio_handlers: &mut event_mgr.mio_handlers,
                        subscriber_idx,
                        subscriber_name,
                        should_stop,
                    },
                });
            }

            fn signals(
                &self,
                _should_stop: &AtomicBool,
                event_mgr: &mut EventManager,
            ) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
                event_mgr.subscribers[self.0].signals()
            }

            fn debug_type_name(&self) -> &'static str {
                self.1
            }
        }

        let mut subscriber_list = self
            .subscribers
            .iter()
            .enumerate()
            .map(|(i, subscriber)| EventManagerSubscriber(i, subscriber.debug_type_name()))
            .collect::<Box<_>>();

        let mut subscriber_ref_list = subscriber_list
            .iter_mut()
            .map(|v| v as &mut dyn SignalMultiplexHandler<EventManager>)
            .collect::<Box<_>>();

        multiplex_signals(
            &AtomicBool::new(false),
            &mut subscriber_ref_list,
            EventManagerParker(self),
        );
    }
}

// InterestCtrl
#[repr(transparent)]
pub struct InterestCtrl<'a, Meta: ?Sized> {
    _ty: PhantomData<fn() -> Meta>,
    raw: InterestCtrlInner<'a>,
}

// We need this inner type to ensure that we can cast the `Meta` phantom parameter safely.
struct InterestCtrlInner<'a> {
    poll: &'a mut mio::Poll,
    mio_handlers: &'a mut Vec<MioHandler>,
    subscriber_idx: usize,
    subscriber_name: &'static str,
    should_stop: &'a AtomicBool,
}

impl<'a, Meta: ?Sized> InterestCtrl<'a, Meta> {
    pub fn cast_meta<NewMeta: ?Sized>(&mut self) -> &mut InterestCtrl<'a, NewMeta> {
        unsafe { &mut *(self as *mut Self as *mut InterestCtrl<'a, NewMeta>) }
    }

    // FIXME: Gruel
    //
    // NOTE: The semantics of this method are a bit weird. It doesn't do anything in `init_interests`
    // since the event manager isn't even running yet and it still allows other events in the event
    // buffer to be handled by subscribers. This behavior was chosen because it's what `polly` did
    // so a more in-depth investigation of this is necessary.
    pub fn stop(&mut self) {
        self.raw.should_stop.store(true, Relaxed);
    }
}

impl<'a, Meta: 'static + Send + Sync> InterestCtrl<'a, Meta> {
    pub fn register(
        &mut self,
        source: &mut impl mio::event::Source,
        interests: mio::Interest,
        metadata: Meta,
    ) {
        let token = mio::Token(self.raw.mio_handlers.len());

        if let Err(err) = self.raw.poll.registry().register(source, token, interests) {
            tracing::error!(
                "`{}` failed to subscribe to an epoll target: {err}",
                self.raw.subscriber_name
            );
        }

        self.raw.mio_handlers.push(MioHandler {
            subscriber_idx: self.raw.subscriber_idx,
            metadata: Some(smallbox::smallbox!(metadata)),
        });
    }
}

impl InterestCtrl<'_, RawFd> {
    pub fn register_fd(&mut self, source: &impl AsRawFd, interests: mio::Interest) {
        let source = source.as_raw_fd();
        self.register(&mut SourceFd(&source), interests, source);
    }
}
