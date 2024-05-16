use core::fmt;
use std::sync::Arc;

use derive_where::derive_where;
use generational_arena::{Arena, Index};
use newt::{NumEnum, NumEnumMap};
use parking_lot::{Condvar, Mutex};
use thiserror::Error;

use crate::{util::ExtensionFor, BoundSignalChannel};

// === Errors === //

#[derive(Debug, Clone, Error)]
#[error("failed to spawn new task: shutdown already requested")]
#[non_exhaustive]
pub struct ShutdownAlreadyRequested;

// === MultiShutdownSignal === //

#[derive_where(Clone, Default)]
pub struct MultiShutdownSignal<P: NumEnum> {
    signals: Arc<NumEnumMap<P, ShutdownSignal>>,
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

    pub fn spawn(
        &self,
        phase: P,
        kick: impl 'static + Send + Sync + FnOnce(),
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested> {
        self.signals[phase].spawn_ref(kick)
    }

    pub fn shutdown(&self) {
        for signal in self.signals.values() {
            signal.shutdown();
        }
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

    pub fn spawn(
        self,
        kick: impl 'static + Send + Sync + FnOnce(),
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested> {
        let mut guard = self.0.state.lock();

        if guard.shutting_down {
            return Err(ShutdownAlreadyRequested);
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

    pub fn spawn_ref(
        &self,
        kick: impl 'static + Send + Sync + FnOnce(),
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested> {
        self.clone().spawn(kick)
    }

    pub fn shutdown(&self) {
        let mut guard = self.0.state.lock();
        guard.shutting_down = true;

        for (_, task) in &mut guard.tasks {
            // This is technically calling userland code but that routine should already be quite
            // reentrancy-aware so this is unlikely to be the source of bugs.
            task();
        }

        self.0.condvar.wait(&mut guard);
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

// === Extensions === //

pub trait MultiShutdownSignalExt: ExtensionFor<MultiShutdownSignal<Self::Phase>> {
    type Phase: NumEnum;

    fn spawn_signal(
        &self,
        phase: Self::Phase,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested>;
}

impl<P: NumEnum> MultiShutdownSignalExt for MultiShutdownSignal<P> {
    type Phase = P;

    fn spawn_signal(
        &self,
        phase: Self::Phase,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested> {
        self.spawn(phase, move || signal.assert())
    }
}

pub trait ShutdownSignalExt: ExtensionFor<ShutdownSignal> {
    fn spawn_signal(
        self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested>;

    fn spawn_signal_ref(
        &self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested>;
}

impl ShutdownSignalExt for ShutdownSignal {
    fn spawn_signal(
        self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested> {
        self.spawn(move || signal.assert())
    }

    fn spawn_signal_ref(
        &self,
        signal: BoundSignalChannel,
    ) -> Result<ShutdownTask, ShutdownAlreadyRequested> {
        self.spawn_ref(move || signal.assert())
    }
}

// === Tests === //

#[cfg(test)]
mod test {
    use std::sync::Barrier;

    use crate::{ParkSignalChannelExt, RawSignalChannel};

    use super::*;

    #[test]
    fn does_notify() {
        let subscriber_barrier = Barrier::new(2);
        let shutdown = ShutdownSignal::new();

        std::thread::scope(|s| {
            s.spawn(|| {
                let signal = RawSignalChannel::new();
                let task = shutdown
                    .clone()
                    .spawn_signal(BoundSignalChannel {
                        channel: signal.clone(),
                        mask: 0b1,
                    })
                    .unwrap();

                subscriber_barrier.wait();

                while signal.take(0b1) == 0 {
                    signal.wait_on_park(u64::MAX);
                }

                assert!(shutdown
                    .clone()
                    .spawn_signal(BoundSignalChannel {
                        channel: signal.clone(),
                        mask: 0b1,
                    })
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
