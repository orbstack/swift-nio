use std::{
    io,
    ptr::NonNull,
    sync::{Arc, OnceLock},
    time::Duration,
};

use parking_lot::Mutex;
use thiserror::Error;

use crate::{util::Parker, AnySignalChannelWith, Waker, WakerIndex};

// === ParkWaker === //

#[derive(Debug, Default)]
pub struct ParkWaker(Parker);

impl Waker for ParkWaker {
    fn wake(&self) {
        self.0.unpark();
    }
}

pub trait ParkSignalChannelExt: AnySignalChannelWith<ParkWaker> {
    fn wait_on_park(&self, mask: Self::Mask) {
        let raw = self.raw();
        let mask = Self::mask_to_u64(mask);

        // Unfortunately, a given `wait` command is allowed to be both skipped and awaken, which can
        // easily lead in a left-over wake-up ticket. Hence, we loop until we're sure that the operation
        // has actually been woken up. In practice, this loop should very rarely be taken.
        while !raw.could_take(mask) {
            raw.wait(mask, WakerIndex::of::<ParkWaker>(), || {
                raw.waker_state::<ParkWaker>().0.park();
            });
        }
    }

    fn wait_on_park_timeout(&self, mask: Self::Mask, timeout: Duration) {
        let raw = self.raw();
        let mask = Self::mask_to_u64(mask);

        raw.wait(mask, WakerIndex::of::<ParkWaker>(), || {
            raw.waker_state::<ParkWaker>().0.park_timeout(timeout);
        });
    }
}

impl<T: AnySignalChannelWith<ParkWaker>> ParkSignalChannelExt for T {}

// === DynamicallyBoundWaker === //

#[derive(Default)]
pub struct DynamicallyBoundWaker {
    waker: Mutex<Option<NonNull<dyn FnMut() + Send + Sync>>>,
}

unsafe impl Send for DynamicallyBoundWaker {}

unsafe impl Sync for DynamicallyBoundWaker {}

impl Waker for DynamicallyBoundWaker {
    fn wake(&self) {
        if let Some(waker) = self.waker.lock().as_mut() {
            (unsafe { waker.as_mut() })()
        }
    }
}

impl DynamicallyBoundWaker {
    pub fn wrap_waker<'a>(
        waker: impl FnOnce() + Send + Sync + 'a,
    ) -> impl FnMut() + Send + Sync + 'a {
        let mut waker = Some(waker);
        move || {
            if let Some(waker) = waker.take() {
                waker()
            }
        }
    }

    #[allow(clippy::missing_safety_doc)]
    pub unsafe fn bind_waker(&self, waker: &mut (impl FnMut() + Send + Sync)) {
        // Unsize the waker
        let waker = unsafe {
            #[allow(clippy::unnecessary_cast)]
            NonNull::new_unchecked(
                waker as *mut (dyn FnMut() + Send + Sync + '_) as *mut (dyn FnMut() + Send + Sync),
            )
        };

        let mut curr_waker = self.waker.lock();
        assert!(curr_waker.is_none());

        *curr_waker = Some(waker);
    }

    pub fn clear_waker(&self) {
        *self.waker.lock() = None;
    }
}

pub trait DynamicallyBoundSignalChannelExt: AnySignalChannelWith<DynamicallyBoundWaker> {
    fn wait_on_closure<R>(
        &self,
        mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        let raw = self.raw();
        let mask = Self::mask_to_u64(mask);

        // Provide it to the interned waker
        let dyn_state = raw.waker_state::<DynamicallyBoundWaker>();
        let mut waker = DynamicallyBoundWaker::wrap_waker(waker);
        unsafe { dyn_state.bind_waker(&mut waker) };

        // Bind an undo scope guard
        let _undo_guard = scopeguard::guard((), |()| {
            dyn_state.clear_waker();
        });

        // Run the actual wait operation
        raw.wait(mask, WakerIndex::of::<DynamicallyBoundWaker>(), worker)
    }
}

impl<T: AnySignalChannelWith<DynamicallyBoundWaker>> DynamicallyBoundSignalChannelExt for T {}

// === QueueRecvSignalChannelExt === //

#[derive(Debug, Copy, Clone, Eq, PartialEq, Error)]
pub enum QueueRecvError {
    #[error("queue senders have all disconnected")]
    HungUp,

    #[error("receive operation was cancelled")]
    Cancelled,
}

pub trait QueueRecvSignalChannelExt: DynamicallyBoundSignalChannelExt {
    #[track_caller]
    fn recv_with_cancel<T>(
        &self,
        mask: Self::Mask,
        receiver: &crossbeam_channel::Receiver<T>,
    ) -> Result<T, QueueRecvError> {
        enum Never {}

        let (cancel_send, cancel_recv) = crossbeam_channel::bounded::<Never>(0);

        self.wait_on_closure(
            mask,
            || drop(cancel_send),
            || {
                crossbeam_channel::select! {
                    recv(receiver) -> val => {
                        val.map_err(|_| QueueRecvError::HungUp)
                    }
                    recv(cancel_recv) -> _ => {
                        Err(QueueRecvError::Cancelled)
                    }
                }
            },
        )
        .unwrap_or(Err(QueueRecvError::Cancelled))
    }
}

impl<T: DynamicallyBoundSignalChannelExt> QueueRecvSignalChannelExt for T {}

// === OnceMioWaker === //

#[derive(Default)]
pub struct OnceMioWaker(OnceLock<Arc<mio::Waker>>);

impl OnceMioWaker {
    pub fn set_waker(&self, waker: Arc<mio::Waker>) {
        self.0
            .set(waker)
            .expect("attempted to set MIO waker more than once")
    }
}

impl Waker for OnceMioWaker {
    fn wake(&self) {
        if let Err(err) = self
            .0
            .get()
            .expect("attempted to `wait_on_poll` before waker was set")
            .wake()
        {
            tracing::error!("Failed to dispatch waker in OnceMioWaker: {err}");
        }
    }
}

pub trait MioChannelExt: AnySignalChannelWith<OnceMioWaker> {
    #[track_caller]
    fn wait_on_poll(
        &self,
        mask: Self::Mask,
        poll: &mut mio::Poll,
        events: &mut mio::Events,
        timeout: Option<Duration>,
    ) -> io::Result<()> {
        debug_assert!(
            self.raw().waker_state().0.get().is_some(),
            "attempted to `wait_on_poll` before waker was set"
        );

        self.raw()
            .wait(
                Self::mask_to_u64(mask),
                WakerIndex::of::<OnceMioWaker>(),
                || poll.poll(events, timeout),
            )
            .unwrap_or(Ok(()))
    }
}

impl<T: AnySignalChannelWith<OnceMioWaker>> MioChannelExt for T {}
