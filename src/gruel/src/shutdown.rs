use core::fmt;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc,
};

use derive_where::derive_where;
use generational_arena::{Arena, Index};
use newt::{NumEnum, NumEnumMap};
use parking_lot::{Condvar, Mutex};
use thiserror::Error;

use crate::{util::ExtensionFor, BoundSignalChannel};

// === Errors === //

#[derive_where(Debug)]
#[derive(Error, Clone)]
#[error("failed to spawn new task: shutdown already requested")]
#[non_exhaustive]
pub struct ShutdownAlreadyRequested<F> {
    #[derive_where(skip)]
    pub kick: F,
}

pub trait ShutdownAlreadyRequestedExt:
    ExtensionFor<Result<ShutdownTask, ShutdownAlreadyRequested<Self::Handler>>>
{
    type Handler: FnOnce();

    fn unwrap_or_run_now(self) -> Option<ShutdownTask>;
}

impl<H: FnOnce()> ShutdownAlreadyRequestedExt
    for Result<ShutdownTask, ShutdownAlreadyRequested<H>>
{
    type Handler = H;

    fn unwrap_or_run_now(self) -> Option<ShutdownTask> {
        match self {
            Ok(handler) => Some(handler),
            Err(err) => {
                (err.kick)();
                None
            }
        }
    }
}

// === MultiShutdownSignal === //

#[derive_where(Clone, Default)]
pub struct MultiShutdownSignal<P: NumEnum> {
    inner: Arc<MultiShutdownSignalInner<P>>,
}

#[derive_where(Default)]
struct MultiShutdownSignalInner<P: NumEnum> {
    is_condemned: AtomicBool,
    signals: NumEnumMap<P, ShutdownSignal>,
}

impl<P: NumEnum> fmt::Debug for MultiShutdownSignal<P> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("MultiShutdownSignal")
            .finish_non_exhaustive()
    }
}

impl<P: NumEnum> MultiShutdownSignal<P> {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn spawn<F>(&self, phase: P, kick: F) -> Result<ShutdownTask, ShutdownAlreadyRequested<F>>
    where
        F: 'static + Send + Sync + FnOnce(),
    {
        self.phase_ref(phase).spawn_ref(kick)
    }

    pub fn phase(&self, phase: P) -> ShutdownSignal {
        self.phase_ref(phase).clone()
    }

    pub fn phase_ref(&self, phase: P) -> &ShutdownSignal {
        &self.inner.signals[phase]
    }

    pub fn shutdown(&self) {
        // We cannot allow races over shutdown since a second shutdown request will skip past what
        // we've shutdown thus far and shut down the next phase early. All other forms of
        // races (especially for binding) are perfectly permissible and, indeed, desired to ensure
        // that new tasks are only rejected once the phase has run.
        if self.inner.is_condemned.swap(true, Ordering::Relaxed) {
            // (we're already condemned)
            return;
        }

        for (phase, signal) in self.inner.signals.iter() {
            log::info!("Beginning shutdown phase: {phase:?}");
            signal.shutdown();
        }
        log::info!("Completed all shutdown phases on `MultiShutdownSignal`");
    }
}

// === ShutdownSignal === //

#[derive(Clone, Default)]
pub struct ShutdownSignal(Arc<ShutdownSignalInner>);

impl fmt::Debug for ShutdownSignal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ShutdownSignal").finish_non_exhaustive()
    }
}

#[derive(Default)]
struct ShutdownSignalInner {
    state: Mutex<State>,
    condvar: Condvar,
}

#[derive(Default)]
struct State {
    shutting_down: bool,
    tasks: Arena<Box<dyn Send + Sync + FnMut()>>,
}

impl ShutdownSignal {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn spawn<F>(self, kick: F) -> Result<ShutdownTask, ShutdownAlreadyRequested<F>>
    where
        F: 'static + Send + Sync + FnOnce(),
    {
        let mut guard = self.0.state.lock();

        if guard.shutting_down {
            return Err(ShutdownAlreadyRequested { kick });
        }

        let mut kick = Some(kick);
        let index = guard.tasks.insert(Box::new(move || {
            if let Some(kick) = kick.take() {
                kick();
            }
        }));

        drop(guard);

        Ok(ShutdownTask {
            signal: self,
            index,
        })
    }

    pub fn spawn_ref<F>(&self, kick: F) -> Result<ShutdownTask, ShutdownAlreadyRequested<F>>
    where
        F: 'static + Send + Sync + FnOnce(),
    {
        self.clone().spawn(kick)
    }

    pub fn shutdown(&self) {
        let mut guard = self.0.state.lock();
        if guard.shutting_down {
            return;
        }
        guard.shutting_down = true;

        for (_, task) in &mut guard.tasks {
            // This is technically calling userland code but that routine should already be quite
            // reentrancy-aware so this is unlikely to be the source of bugs.
            task();
        }

        if !guard.tasks.is_empty() {
            self.0.condvar.wait(&mut guard);
        }

        assert!(guard.tasks.is_empty());
    }
}

// === ShutdownTask === //

#[derive(Debug)]
pub struct ShutdownTask {
    signal: ShutdownSignal,
    index: Index,
}

impl ShutdownTask {
    pub fn signal(&self) -> &ShutdownSignal {
        &self.signal
    }
}

impl Drop for ShutdownTask {
    fn drop(&mut self) {
        let mut guard = self.signal.0.state.lock();

        guard.tasks.remove(self.index);
        if guard.shutting_down && guard.tasks.is_empty() {
            self.signal.0.condvar.notify_all();
        }
    }
}

// === Integrations === //

pub trait MultiShutdownSignalExt: ExtensionFor<MultiShutdownSignal<Self::Phase>> {
    type Phase: NumEnum;

    fn spawn_signal(
        &self,
        phase: Self::Phase,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested<impl 'static + Send + Sync + FnOnce()>>;
}

impl<P: NumEnum> MultiShutdownSignalExt for MultiShutdownSignal<P> {
    type Phase = P;

    fn spawn_signal(
        &self,
        phase: Self::Phase,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested<impl 'static + Send + Sync + FnOnce()>> {
        self.spawn(phase, move || signal.assert())
    }
}

pub trait ShutdownSignalExt: ExtensionFor<ShutdownSignal> {
    fn spawn_signal(
        self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested<impl 'static + Send + Sync + FnOnce()>>;

    fn spawn_signal_ref(
        &self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested<impl 'static + Send + Sync + FnOnce()>>;
}

impl ShutdownSignalExt for ShutdownSignal {
    fn spawn_signal(
        self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested<impl 'static + Send + Sync + FnOnce()>> {
        self.spawn(move || signal.assert())
    }

    fn spawn_signal_ref(
        &self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested<impl 'static + Send + Sync + FnOnce()>> {
        self.spawn_ref(move || signal.assert())
    }
}

// === Tests === //

#[cfg(test)]
mod test {
    use std::sync::Barrier;

    use crate::{define_waker_set, ParkSignalChannelExt, ParkWaker, RawSignalChannel};

    use super::*;

    define_waker_set! {
        #[derive(Default)]
        struct MyWakerSet {
            park: ParkWaker,
        }
    }

    #[test]
    fn does_notify() {
        let subscriber_barrier = Barrier::new(2);
        let shutdown = ShutdownSignal::new();

        std::thread::scope(|s| {
            s.spawn(|| {
                let signal = RawSignalChannel::new(MyWakerSet::default());
                let task = shutdown
                    .clone()
                    .spawn_signal(signal.bind_clone(0b1))
                    .unwrap();

                subscriber_barrier.wait();

                while signal.take(0b1) == 0 {
                    signal.wait_on_park();
                }

                assert!(shutdown
                    .clone()
                    .spawn_signal(signal.bind_clone(0b1))
                    .is_err());

                drop(task);
            });

            s.spawn(|| {
                subscriber_barrier.wait();
                shutdown.shutdown();
            });
        });
    }
}
