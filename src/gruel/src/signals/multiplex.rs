use std::sync::{atomic::AtomicBool, Arc};

use memmage::CloneDynRef;

use crate::{util::Parker, DynamicallyBoundWaker, RawSignalChannel, ShutdownSignal};

#[cfg(not(loom))]
use std::sync::atomic::{AtomicU64, Ordering::*};

#[cfg(loom)]
use loom::sync::atomic::{AtomicU64, Ordering::*};

pub fn process_signals_multiplexed_park(
    shutdown: &ShutdownSignal,
    handlers: &mut [&mut dyn SignalMultiplexHandler],
) {
    let should_stop = Arc::new(AtomicBool::new(false));
    let parker = Arc::new(Parker::default());

    let _task = shutdown.spawn_ref({
        let should_stop = should_stop.clone();
        let parker = parker.clone();

        move || {
            should_stop.store(true, Relaxed);
            parker.unpark();
        }
    });

    process_signals_multiplexed(handlers, &should_stop, || parker.park(), || parker.unpark());
}

pub fn process_signals_multiplexed(
    handlers: &mut [&mut dyn SignalMultiplexHandler],
    should_stop: &AtomicBool,
    mut park: impl FnMut(),
    unpark: impl Sync + Fn(),
) {
    // Create a bitflag for quickly determining which handlers are dirty.
    let dirty_flags = (0..(handlers.len() + 63) / 64)
        .map(|_| AtomicU64::new(0))
        .collect::<Box<[_]>>();

    let dirty_flags = &*dirty_flags;

    // Create a parker for this subscriber loop.
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
                    handlers[i].process();
                });

                #[cfg(debug_assertions)]
                for (signal_idx, signal) in handler_signals[i_cell].iter().enumerate() {
                    if signal.could_take(u64::MAX) {
                        tracing::warn!(
                            "Signal multiplex handler of type `{}` ignored some events from its subscribed \
                             signal at index {signal_idx}; this could cause unexpected behavior.",
                            handlers[i].debug_type_name(),
                        );
                    }
                }
            }
        }

        // Although we don't redo another `wait` operation here, this routine is still guaranteed not
        // to miss any events because `park` holds unpark tickets.
        (park)();
    }
}

pub trait SignalMultiplexHandler: 'static {
    fn process(&mut self);

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>>;

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<Self>()
    }
}

impl<T: ?Sized + SignalMultiplexHandler> SignalMultiplexHandler for Arc<std::sync::Mutex<T>> {
    fn process(&mut self) {
        self.lock().unwrap().process();
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.lock().unwrap().signals()
    }
}

impl<T: ?Sized + SignalMultiplexHandler> SignalMultiplexHandler for Arc<std::sync::RwLock<T>> {
    fn process(&mut self) {
        self.write().unwrap().process();
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.read().unwrap().signals()
    }
}

impl<T: ?Sized + SignalMultiplexHandler> SignalMultiplexHandler
    for std::rc::Rc<std::cell::RefCell<T>>
{
    fn process(&mut self) {
        self.borrow_mut().process();
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.borrow().signals()
    }
}
