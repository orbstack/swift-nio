use std::fmt;

// === ExtensionFor === //

pub trait ExtensionFor<T: ?Sized> {}

impl<T: ?Sized> ExtensionFor<T> for T {}

// === Formatting Helpers === //

pub struct FmtDebugUsingDisplay<T>(pub T);

impl<T: fmt::Display> fmt::Debug for FmtDebugUsingDisplay<T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.0.fmt(f)
    }
}

pub struct FmtU64AsBits(pub u64);

impl fmt::Debug for FmtU64AsBits {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{:b}", self.0)
    }
}

// === Parker === //

cfgenius::define! {
    enable_specialized_park = true()
}

cfgenius::cond! {
    if cfg(loom) {
        // Loom implementation
        mod park {
            use std::time::Duration;

            #[cfg(loom)]
            use loom::sync::{Mutex, Condvar};

            #[derive(Debug, Default)]
            pub struct Parker {
                state: Mutex<bool>,
                condvar: Condvar,
            }

            impl Parker {
                pub fn park(&self) {
                    let mut state = self.state.lock().unwrap();

                    while !*state {
                        state = self.condvar.wait(state).unwrap();
                    }

                    *state = false;
                }

                pub fn park_timeout(&self, _timeout: Duration) {
                    // (loom has no notion of timeouts)
                }

                pub fn unpark(&self) {
                    *self.state.lock().unwrap() = true;
                    self.condvar.notify_all();
                }
            }
        }
    } else if all(cfg(target_os = "macos"), macro(enable_specialized_park)) {
        // MacOS
        pub use sysx::sync::parker as park;
    } else {
        // Fallback
        mod park {
            use std::{time::Duration, sync::{Condvar, Mutex}};

            #[derive(Debug, Default)]
            pub struct Parker {
                state: Mutex<bool>,
                condvar: Condvar,
            }

            impl Parker {
                pub fn park(&self) {
                    let mut state = self.state.lock().unwrap();

                    while !*state {
                        state = self.condvar.wait(state).unwrap();
                    }

                    *state = false;
                }

                pub fn park_timeout(&self, timeout: Duration) {
                    let mut state = self.state.lock().unwrap();
                    if !*state {
                        state = self.condvar.wait_timeout(state, timeout).unwrap().0;
                    }
                    *state = false;
                }

                pub fn unpark(&self) {
                    *self.state.lock().unwrap() = true;
                    self.condvar.notify_all();
                }
            }
        }
    }
}

pub use park::Parker;
