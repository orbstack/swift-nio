use std::{
    sync::atomic::{AtomicI8, Ordering::*},
    time::Duration,
};

#[allow(non_camel_case_types)]
type dispatch_semaphore_t = *mut std::ffi::c_void;
#[allow(non_camel_case_types)]
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
pub enum ParkResult {
    Unparked,
    TimedOut,
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

        Self {
            semaphore,
            state: AtomicI8::new(EMPTY),
        }
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

    pub fn park_timeout(&self, timeout: Duration) -> ParkResult {
        if self.state.fetch_sub(1, Acquire) == NOTIFIED {
            return ParkResult::Unparked;
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
            ParkResult::Unparked
        } else {
            // Either a timeout occurred and we reset the state before any thread
            // tried to wake us up, or we were woken up and reset the state,
            // making sure to observe the state change with acquire ordering.
            // Either way, the semaphore counter is now zero again.
            if timeout {
                ParkResult::TimedOut
            } else {
                ParkResult::Unparked
            }
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
