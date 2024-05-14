// === ExtensionFor === //

pub trait ExtensionFor<T: ?Sized> {}

impl<T: ?Sized> ExtensionFor<T> for T {}

// === Parker === //

cfgenius::define! {
    enable_specialized_park = true()
}

cfgenius::cond! {
    if all(cfg(target_os = "macos"), macro(enable_specialized_park)) {
        // MacOS
        // Reference: `src/sys/pal/unix/thread_parking/darwin.rs` in `std`
        #[allow(non_camel_case_types)]
        mod park {
            use std::sync::atomic::{AtomicI8, Ordering::*};

            type dispatch_semaphore_t = *mut std::ffi::c_void;
            type dispatch_time_t = u64;

            const DISPATCH_TIME_FOREVER: dispatch_time_t = !0;

            const EMPTY: i8 = 0;
            const NOTIFIED: i8 = 1;
            const PARKED: i8 = -1;

            // Contained in libSystem.dylib, which is linked by default.
            // TODO: Bindings?
            extern "C" {
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
            use std::thread;

            #[derive(Debug)]
            pub struct GracelessParker(thread::Thread);

            impl Default for Parker {
                fn default() -> Self {
                    Self(thread::current())
                }
            }

            impl Parker {
                pub fn park(&self) {
                    thread::park();
                }

                pub fn unpark(&self) {
                    self.0.unpark();
                }
            }
        }
    }
}

pub use park::Parker;
