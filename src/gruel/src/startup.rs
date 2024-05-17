use std::{fmt, sync::Arc};

use parking_lot::{Condvar, Mutex};
use thiserror::Error;

// === State === //

#[derive(Default)]
struct StartupInner {
    state: Mutex<State>,
    condvar: Condvar,
}

struct State {
    // `0` means done, `usize::MAX` means aborted
    pending_starts: usize,
}

impl Default for State {
    fn default() -> Self {
        Self { pending_starts: 1 }
    }
}

impl StartupInner {
    pub fn abort(&self) {
        let mut state = self.state.lock();
        if state.pending_starts == 0 || state.pending_starts == usize::MAX {
            return;
        }
        state.pending_starts = usize::MAX;
        drop(state);

        self.condvar.notify_all();
    }
}

impl State {
    pub fn interpret_as_result(&self) -> Option<Result<(), StartupAbortedError>> {
        match self.pending_starts {
            0 => Some(Ok(())),
            usize::MAX => Some(Err(StartupAbortedError)),
            _ => None,
        }
    }
}

// === StartupSignal === //

#[derive(Debug, Error, Clone)]
#[error("startup aborted")]
#[non_exhaustive]
pub struct StartupAbortedError;

#[derive(Clone)]
#[must_use]
pub struct StartupSignal(Arc<StartupInner>);

impl fmt::Debug for StartupSignal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("StartupSignal").finish_non_exhaustive()
    }
}

impl StartupSignal {
    pub fn new() -> (Self, StartupTask) {
        let state = Arc::<StartupInner>::default();
        (Self(state.clone()), StartupTask(state))
    }

    pub fn abort(&self) {
        self.0.abort();
    }

    pub fn wait(&self) -> Result<(), StartupAbortedError> {
        let mut guard = self.0.state.lock();

        if let Some(early) = guard.interpret_as_result() {
            return early;
        }

        self.0.condvar.wait(&mut guard);

        guard.interpret_as_result().unwrap()
    }
}

// === StartupTask === //

#[must_use]
pub struct StartupTask(Arc<StartupInner>);

impl Clone for StartupTask {
    fn clone(&self) -> Self {
        let clone = Self(self.0.clone());
        {
            let mut state = clone.0.state.lock();
            debug_assert!(state.pending_starts != 0);

            state.pending_starts = state.pending_starts.saturating_add(1);
            // ^^^ this is just a funny way to express...
            //
            // if state.pending_starts != usize::MAX {
            //     state.pending_starts += 1;
            // }
        }
        clone
    }
}

impl StartupTask {
    pub fn success(self) {
        let mut state = self.0.state.lock();

        if state.pending_starts != usize::MAX {
            state.pending_starts -= 1;
        }

        let should_notify = state.pending_starts == 0;
        drop(state);

        if should_notify {
            self.0.condvar.notify_all();
        }
    }
}

impl Drop for StartupTask {
    fn drop(&mut self) {
        self.0.abort();
    }
}

// === Tests === //

#[cfg(test)]
mod tests {
    use std::thread;

    use super::*;

    #[test]
    fn simple_usage_success() {
        let (signal, task) = StartupSignal::new();

        thread::scope(|s| {
            s.spawn(|| {
                assert!(signal.wait().is_ok());
            });

            s.spawn(|| {
                task.success();
            });
        });
    }

    #[test]
    fn simple_usage_failure() {
        let (signal, task) = StartupSignal::new();

        thread::scope(|s| {
            s.spawn(|| {
                assert!(signal.wait().is_err());
            });

            s.spawn(|| {
                drop(task);
            });
        });
    }

    #[test]
    fn simple_usage_cloned_tasks() {
        let (signal, task) = StartupSignal::new();

        thread::scope(|s| {
            s.spawn(|| {
                assert!(signal.wait().is_err());
            });

            s.spawn({
                let task = task.clone();
                || {
                    task.success();
                }
            });

            s.spawn(|| {
                drop(task);
            });
        });
    }
}
