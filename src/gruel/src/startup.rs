use std::{
    fmt,
    mem::{self, ManuallyDrop},
    sync::Arc,
};

use parking_lot::{Condvar, Mutex};
use thiserror::Error;

// === State === //

struct StartupInner {
    // N.B. we use parking-lot's mutex to avoid spurious wake-ups since they harm correctness.
    state: Mutex<State>,
    condvar: Condvar,
}

struct State {
    // `0` means done, `usize::MAX` means aborted
    pending_starts: usize,
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

impl Default for StartupSignal {
    fn default() -> Self {
        Self(Arc::new(StartupInner {
            state: Mutex::new(State { pending_starts: 0 }),
            condvar: Condvar::new(),
        }))
    }
}

impl StartupSignal {
    pub fn new() -> (Self, StartupTask) {
        let state = Arc::new(StartupInner {
            state: Mutex::new(State { pending_starts: 1 }),
            condvar: Condvar::new(),
        });

        (Self(state.clone()), StartupTask(ManuallyDrop::new(state)))
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

        // N.B. it is possible that a thread may race the `condvar` notification here and resurrect
        // the signal after a successful or unsuccessful assertion. Hence, we may have to handle values
        // other than `0` or `usize::MAX`. This has the expected behavior except for when the signal
        // is resurrected and then aborted, in which case, it will report the abort rather than the
        // initial success. This is acceptable because `abort` is always an error scenario.
        //
        // Note: `condvar.wait` will only wake-up if the signal truly ever has gone to zero since
        // `parking_lot` does not allow spurious wake-ups.
        guard.interpret_as_result().unwrap_or(Ok(()))
    }

    pub fn resurrect(self) -> Result<StartupTask, StartupAbortedError> {
        // Increment RC
        // This is not exactly the same of the clone handler, hence the
        // duplication.
        {
            let mut guard = self.0.state.lock();

            if guard.pending_starts == usize::MAX {
                return Err(StartupAbortedError);
            } else {
                guard.pending_starts += 1;
            }
        }

        Ok(StartupTask(ManuallyDrop::new(self.0)))
    }

    pub fn resurrect_cloned(&self) -> Result<StartupTask, StartupAbortedError> {
        self.clone().resurrect()
    }
}

// === StartupTask === //

#[must_use]
pub struct StartupTask(ManuallyDrop<Arc<StartupInner>>);

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
    fn into_signal_no_rc(mut self) -> StartupSignal {
        let signal = StartupSignal(unsafe { ManuallyDrop::take(&mut self.0) });
        mem::forget(self);
        signal
    }

    pub fn success(self) {
        let _ = self.success_keeping();
    }

    pub fn success_keeping(self) -> StartupSignal {
        let signal = self.into_signal_no_rc();

        let mut state = signal.0.state.lock();

        if state.pending_starts != usize::MAX {
            state.pending_starts -= 1;
        }

        let should_notify = state.pending_starts == 0;
        drop(state);

        if should_notify {
            signal.0.condvar.notify_all();
        }

        signal
    }

    pub fn abort(self) {
        drop(self);
    }

    pub fn abort_ref(&self) {
        self.0.abort();
    }
}

impl Drop for StartupTask {
    fn drop(&mut self) {
        self.abort_ref();
        unsafe { ManuallyDrop::drop(&mut self.0) }
    }
}

// === Tests === //

#[cfg(all(test, not(loom)))]
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

    #[test]
    fn test_task_resurrection() {
        let (signal, task) = StartupSignal::new();

        let task = task.success_keeping();
        assert!(signal.wait().is_ok());
        drop(task.resurrect());
        assert!(signal.wait().is_err());
    }
}
