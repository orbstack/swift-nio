use std::{fmt, mem, sync::Arc};

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

// === Arc casting === //

pub fn cast_arc<T: ?Sized, V: ?Sized>(arc: Arc<T>, convert: impl FnOnce(&T) -> &V) -> Arc<V> {
    let original = &*arc;
    let converted = convert(original);

    assert_eq!(
        original as *const T as *const (),
        converted as *const V as *const ()
    );
    assert_eq!(mem::size_of_val(original), mem::size_of_val(converted));

    let converted = converted as *const V;

    let _ = Arc::into_raw(arc);
    unsafe { Arc::from_raw(converted) }
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
        // Reference: `src/sys/pal/unix/thread_parking/darwin.rs` in `std`
        #[allow(non_camel_case_types)]
        mod park {
            use std::{
                sync::atomic::{AtomicI8, Ordering::*},
                time::Duration,
            };

            type dispatch_semaphore_t = *mut std::ffi::c_void;
            type dispatch_time_t = u64;

            const DISPATCH_TIME_NOW: dispatch_time_t = 0;
            const DISPATCH_TIME_FOREVER: dispatch_time_t = !0;

            const EMPTY: i8 = 0;
            const NOTIFIED: i8 = 1;
            const PARKED: i8 = -1;

            // Contained in libSystem.dylib, which is linked by default.
            // TODO: Bindings?
            extern "C" {
                fn dispatch_time(when: dispatch_time_t, delta: i64) -> dispatch_time_t;
                fn dispatch_semaphore_create(val: isize) -> dispatch_semaphore_t;
                fn dispatch_semaphore_wait(dsema: dispatch_semaphore_t, timeout: dispatch_time_t) -> isize;
                fn dispatch_semaphore_signal(dsema: dispatch_semaphore_t) -> isize;
                fn dispatch_release(object: *mut std::ffi::c_void);
            }

            #[derive(Debug)]
            pub struct Parker {
                semaphore: dispatch_semaphore_t,
                state: AtomicI8,
            }

            unsafe impl Send for Parker {}
            unsafe impl Sync for Parker {}

            impl Default for Parker {
                fn default() -> Self {
                    let semaphore = unsafe { dispatch_semaphore_create(0) };
                    assert!(!semaphore.is_null(), "failed to create Parker");

                    Self { semaphore, state: AtomicI8::new(EMPTY) }
                }
            }

            impl Parker {
                pub fn park(&self) {
                    if self.state.fetch_sub(1, Acquire) == NOTIFIED {
                        return;
                    }

                    while unsafe { dispatch_semaphore_wait(self.semaphore, DISPATCH_TIME_FOREVER) } != 0 {}

                    self.state.swap(EMPTY, Acquire);
                }

                pub fn park_timeout(&self, timeout: Duration) {
                    if self.state.fetch_sub(1, Acquire) == NOTIFIED {
                        return;
                    }

                    let nanos = timeout.as_nanos().try_into().unwrap_or(i64::MAX);
                    let timeout = unsafe { dispatch_time(DISPATCH_TIME_NOW, nanos) };

                    let timeout = unsafe { dispatch_semaphore_wait(self.semaphore, timeout) != 0 };

                    let state = self.state.swap(EMPTY, Acquire);
                    if state == NOTIFIED && timeout {
                        // If the state was NOTIFIED but semaphore_wait returned without
                        // decrementing the count because of a timeout, it means another
                        // thread is about to call semaphore_signal. We must wait for that
                        // to happen to ensure the semaphore count is reset.
                        while unsafe { dispatch_semaphore_wait(self.semaphore, DISPATCH_TIME_FOREVER) } != 0 {}
                    } else {
                        // Either a timeout occurred and we reset the state before any thread
                        // tried to wake us up, or we were woken up and reset the state,
                        // making sure to observe the state change with acquire ordering.
                        // Either way, the semaphore counter is now zero again.
                    }
                }

                pub fn unpark(&self) {
                    let state = self.state.swap(NOTIFIED, Release);
                    if state == PARKED {
                        unsafe {
                            dispatch_semaphore_signal(self.semaphore);
                        }
                    }
                }
            }

            impl Drop for Parker {
                fn drop(&mut self) {
                    unsafe { dispatch_release(self.semaphore) };
                }
            }
        }
    } else {
        // Fallback
        mod park {
            use std::time::Duration;

            #[derive(Debug, Default)]
            pub struct Parker {
                state: parking_lot::Mutex<bool>,
                condvar: parking_lot::Condvar,
            }

            impl Parker {
                pub fn park(&self) {
                    let mut state = self.state.lock();

                    while !*state {
                        self.condvar.wait(&mut state);
                    }

                    *state = false;
                }

                pub fn park_timeout(&self, timeout: Duration) {
                    let mut state = self.state.lock();
                    if !*state {
                        self.condvar.wait_for(&mut state, timeout);
                    }
                    *state = false;
                }

                pub fn unpark(&self) {
                    *self.state.lock() = true;
                    self.condvar.notify_all();
                }
            }
        }
    }
}

pub use park::Parker;
